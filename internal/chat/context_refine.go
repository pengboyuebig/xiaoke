// 上下文精修（对标 ZCode 的 microcompact / runtime-reminders）：
//
//  1. Microcompact：被动裁剪之前的一层廉价压缩——历史逼近预算时，把旧的
//     工具结果替换为占位符（保留最近 5 组、错误与隐藏消息保留）。纯请求
//     组装期变换，不落库：导出/回收站里的完整历史不受影响。
//  2. system-reminder 管道：todo 提醒 / plan 模式提醒 / 日期变更提醒统一
//     走 <system-reminder> 包裹（嵌套标签转义），只在组装时注入。
package chat

import (
	"fmt"
	"strings"
	"time"

	"xiaoke/internal/store"
)

// cnDate 与 prompt 包同一格式（「2026年10月31日 星期六」）。
func cnDate(t time.Time) string {
	week := []string{"日", "一", "二", "三", "四", "五", "六"}[int(t.Weekday())]
	return fmt.Sprintf("%d年%d月%d日 星期%s", t.Year(), int(t.Month()), t.Day(), week)
}

// joinReminders 串接多段提醒（空段跳过）。
func joinReminders(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return existing + "\n\n" + addition
}

const (
	// microcompactTriggerRatio 触发水位（占字节预算的百分比）。
	microcompactTriggerRatio = 90
	// microcompactKeepGroups 保留最近多少个 assistant 组的工具结果。
	microcompactKeepGroups = 5
	// microcompactMinSavings 节省低于这个字节数就放弃（不值得弄脏历史视图）。
	microcompactMinSavings = 2 * 1024
	// clearedToolResultPlaceholder 是被清除的工具结果占位文本。
	clearedToolResultPlaceholder = "[旧工具结果已清除：早前的工具输出为节省上下文被移除，需要时重新调用工具获取]"
)

// microcompact 把较早的工具结果替换为占位符（请求组装期，不落库）。
//
// 分组规则：每条带 ToolCalls 的 assistant 消息开一组，其后相邻的 tool 结果
// 归入该组；保留最近 microcompactKeepGroups 组，更早组的 tool 消息替换内容。
// 保留例外：Hidden 消息、非 0 序号外的一切错误标记文本（含「工具执行出错」）保留。
// 确定性：同一输入永远产出同一输出。
func microcompact(msgs []store.Message, byteBudget int) []store.Message {
	if byteBudget <= 0 || len(msgs) == 0 {
		return msgs
	}
	if historyBytes(msgs) < byteBudget*microcompactTriggerRatio/100 {
		return msgs // 还没逼近预算，不动
	}
	// 找出所有组的 tool 消息下标：groupStarts[i] = 第 i 组第一条消息下标
	type group struct {
		toolIdx []int
	}
	var groups []group
	cur := -1
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			groups = append(groups, group{})
			cur = len(groups) - 1
			continue
		}
		if m.Role == "tool" && cur >= 0 {
			groups[cur].toolIdx = append(groups[cur].toolIdx, i)
		}
	}
	if len(groups) <= microcompactKeepGroups {
		return msgs
	}
	cut := groups[:len(groups)-microcompactKeepGroups]
	out := make([]store.Message, len(msgs))
	copy(out, msgs)
	saved := 0
	cleared := map[int]bool{}
	for _, g := range cut {
		for _, idx := range g.toolIdx {
			if out[idx].Hidden {
				continue
			}
			if strings.Contains(out[idx].Content, "工具执行出错") {
				continue // 错误结果保留：模型最需要的教训
			}
			saved += len(out[idx].Content) - len(clearedToolResultPlaceholder)
			cleared[idx] = true
		}
	}
	if saved < microcompactMinSavings {
		return msgs // 清了也省不了多少，保持原样
	}
	for idx := range cleared {
		out[idx].Content = clearedToolResultPlaceholder
	}
	return out
}

// systemReminder 把运行时提示包裹成 <system-reminder> 段。
// 嵌套标签转义防止内容伪造框架边界；空内容返回空串（不产生空 reminder）。
func systemReminder(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	content = strings.ReplaceAll(content, "<system-reminder", "&lt;system-reminder")
	content = strings.ReplaceAll(content, "</system-reminder", "&lt;/system-reminder")
	return "<system-reminder>\n" + content + "\n</system-reminder>"
}

// planReminderText plan 模式的稀疏提醒：长会话里只读约束容易被忘。
const planReminderText = "Plan mode still active. Read-only: 只允许调研（list_files/read_file/search_files 与只读 MCP），写文件与执行命令被机制禁用。按「现状调研 → 实施步骤 → 风险与边界」输出或更新计划，等用户批准；催你动手也不行。"

// dateChangeReminderText 跨天提醒（模型的时间感会漂移）。
func dateChangeReminderText(now time.Time) string {
	return "注意：已跨入新的一天，今天是 " + cnDate(now) + "。涉及「今天/最近」的任务以此为准。"
}

// buildReminders 组装本轮需要的全部 reminder（追加为一条 system 消息）。
// userMsgCount 是当前历史里真实用户消息数；lastUserDay 是最后一条用户消息的日期（零值 = 无）。
func buildReminders(planMode bool, userMsgCount int, lastUserDay time.Time, now time.Time) string {
	var parts []string
	if planMode && userMsgCount > 0 && userMsgCount%5 == 0 {
		parts = append(parts, planReminderText)
	}
	if !lastUserDay.IsZero() && (now.Year() != lastUserDay.Year() || now.YearDay() != lastUserDay.YearDay()) {
		parts = append(parts, dateChangeReminderText(now))
	}
	if len(parts) == 0 {
		return ""
	}
	return systemReminder(strings.Join(parts, "\n\n"))
}

// todoReminderText todo 提醒的正文（复用现有 nudge 启发式判断后调用）。
func todoReminderText(todos []store.Todo) string {
	if len(todos) == 0 {
		return "当前任务看起来有多个步骤，而待办清单还是空的。建议先用 todo_write 建立清单（pending/in_progress/completed），随做随更——这是长任务不迷路的机制保障。"
	}
	return "距上次更新待办已有段时间。当前清单：\n" + todosText(todos) + "\n完成一项就立刻更新状态。"
}
