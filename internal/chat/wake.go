// 后台任务完成自动唤醒（对标 opencode 的 Job 完成注入 synthetic 消息）：
// run_command is_background 的任务结束时，若其所属会话空闲，自动把
// 「任务结果摘要」作为一条说明消息注入并跑一轮对话，模型接着处理结果。
// 没有它，模型只能靠 task_output 轮询——而提示词恰恰禁止轮询，形成死等。
package chat

import (
	"context"
	"fmt"
	"log"

	"xiaoke/internal/tools"
)

// taskWakeCooldown 同一会话两次任务唤醒的最小间隔：多个任务几乎同时结束时
// 第一轮唤醒通常会一起汇报，后续唤醒不再重复打断。
// 简化实现：靠会话锁自然排队——第二个唤醒 TryLock 失败直接放弃（不排队），
// 它的结果会被第一轮唤醒后的模型用 task_output 主动查询覆盖。

// wakeSessionForTask 在后台任务结束时被调用（任务 goroutine）。
// 只在会话空闲时唤醒；任何失败仅记日志——唤醒是锦上添花，不能吵醒正在睡觉的人。
func (h *Handler) wakeSessionForTask(sessionID, taskID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("后台任务唤醒 panic（已忽略）: %v", r)
		}
	}()
	cmdline, output, state, ok := tools.BackgroundTaskResult(taskID)
	if !ok || state == "running" {
		return
	}
	// 会话已删除（或不存在）不唤醒；正在生成回复时不打断——模型手头的
	// 对话结束后自然可以从上下文里看到任务已完成的痕迹，或用 task_output 查询。
	if _, err := h.store.Get(sessionID); err != nil {
		return
	}
	unlock, locked := h.store.TryLockSession(sessionID)
	if !locked {
		return
	}
	defer unlock()

	statusText := "已成功完成"
	if state == "failed" {
		statusText = "已结束（退出码非 0，输出里可能包含报错）"
	}
	content := fmt.Sprintf(
		"（系统通知：后台任务 %s %s。\n$ %s\n任务输出：\n%s\n请检查结果；有后续步骤就继续执行，没有就简要汇报即可。不要重新运行这条命令。）",
		taskID, statusText, cmdline, truncateBytesUTF8(output, 4096))

	ctx, cancel := context.WithTimeout(context.Background(), automationRunTimeout)
	defer cancel()
	sink := func(map[string]any) {} // 无人消费事件；回复落库，前端打开会话即可见
	// NoTaskWake：唤醒对话里再启动的后台任务不再嵌套唤醒（防失控循环）
	if err := h.converse(ctx, converseParams{
		SessionID:  sessionID,
		Content:    content,
		Mode:       "build",
		NoTaskWake: true,
	}, sink); err != nil {
		log.Printf("后台任务 %s 唤醒会话 %s 失败: %v", taskID, sessionID, err)
	}
}
