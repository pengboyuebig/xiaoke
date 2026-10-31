// 定时任务调度器：把 cron 包的「到期领取」接到 converse 的无人值守执行。
//
// 运行语义：
//   - 每 30s tick 一次，锁内领取到期任务（防重复触发）
//   - 每次运行新建独立会话（标题「⏰ 任务名」），走完整 agent 循环
//   - 无人值守：ask_user 立即返回「用户不在线，自行决策」；权限门关闭（build 语义）
//   - 防递归：会话 params 带 AutomationID → 会话内 cron_create 直接拒绝
//   - 事件以日志落地（无人看 SSE）；失败记入 LastError，不影响下次调度
package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"xiaoke/internal/cron"
)

const automationTick = 30 * time.Second

// StartAutomationLoop 启动调度 goroutine（不阻塞启动；cron 未配置时为空转）。
func (h *Handler) StartAutomationLoop() {
	if h.cron == nil {
		return
	}
	go func() {
		t := time.NewTicker(automationTick)
		defer t.Stop()
		for range t.C {
			h.runDueAutomations()
		}
	}()
}

// runDueAutomations 领取并串行执行到期任务（单调度器，无并发）。
func (h *Handler) runDueAutomations() {
	due := h.cron.Due(time.Now())
	for _, a := range due {
		h.runAutomation(a)
	}
}

// runAutomation 跑一个到期任务：建会话 → converse → 记录结果。
func (h *Handler) runAutomation(a cron.Automation) {
	sess, err := h.store.Create("⏰ "+a.Title, a.ProjectID)
	if err != nil {
		h.cron.RecordError(a.ID, "创建会话失败: "+err.Error())
		return
	}
	unlockSess, ok := h.store.TryLockSession(sess.ID)
	if !ok {
		h.cron.RecordError(a.ID, "会话锁被占用，跳过本次")
		return
	}
	defer unlockSess()

	started := time.Now()
	var sb strings.Builder
	sink := func(evt map[string]any) {
		switch evt["type"] {
		case "tool_result":
			if ok, _ := evt["ok"].(bool); !ok {
				fmt.Fprintf(&sb, "工具失败 %v: %v\n", evt["name"], evt["error"])
			}
		case "error":
			fmt.Fprintf(&sb, "错误: %v\n", evt["error"])
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), automationRunTimeout)
	defer cancel()
	err = h.converse(ctx, converseParams{
		SessionID:    sess.ID,
		Content:      a.Prompt,
		ProjectID:    a.ProjectID,
		Mode:         "build",
		AutomationID: a.ID,
	}, sink)
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		log.Printf("定时任务 %s（%s）执行失败: %v", a.Title, a.ID, err)
		h.cron.RecordError(a.ID, err.Error())
		return
	}
	log.Printf("定时任务 %s（%s）已运行（用时 %s）", a.Title, a.ID, time.Since(started).Round(time.Second))
	if sb.Len() > 0 {
		h.cron.RecordError(a.ID, strings.TrimSpace(sb.String()))
	}
}

// automationRunTimeout 是单次无人值守运行的硬上限（防任务失控常驻）。
const automationRunTimeout = 15 * time.Minute
