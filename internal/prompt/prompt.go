// Package prompt 组装系统提示词：人设 + 智能体人格 + 工具规范 + 技能 + 项目上下文。
package prompt

import (
	"fmt"
	"strings"

	"xiaoke/internal/persona"
)

// Request 是一次组装所需的全部上下文。
type Request struct {
	AgentName      string // 智能体名字（默认「小柯」）
	AgentBase      string // 智能体自带人格（空 = 用默认小柯人设）
	AgentTools     bool   // 该智能体是否声明使用工具
	ProjectName    string // 当前项目（空 = 未选择）
	ProjectPath    string
	ReadDirs       []string // 只读授权目录
	ToolsAvailable bool     // 本次会话实际可用工具（内置+MCP > 0）
	SkillsAssembly string   // 启用技能的注入正文
	Instructions   string   // 追加的补充指令
}

// Build 组装最终系统提示词。
func Build(r Request) string {
	var b strings.Builder

	// 1) 人格：智能体自带优先，否则默认小柯人设（替换成智能体名字）
	base := strings.TrimSpace(r.AgentBase)
	if base == "" {
		base = strings.Replace(persona.SystemPrompt, persona.Name, r.AgentName, 1)
	} else {
		base = strings.ReplaceAll(base, persona.Name, r.AgentName)
	}
	b.WriteString(base)

	// 2) 工具规范：智能体声明用工具 或 会话确实有工具时注入
	if r.AgentTools || r.ToolsAvailable {
		b.WriteString(persona.ToolGuide)
		if !r.AgentTools && r.ToolsAvailable {
			b.WriteString("\n（当前智能体未声明使用工具，但本会话工具可用，按需调用。）")
		}
	}

	// 3) 技能
	if s := strings.TrimSpace(r.SkillsAssembly); s != "" {
		b.WriteString(s)
	}

	// 4) 项目上下文
	if r.ProjectName != "" {
		fmt.Fprintf(&b, "\n\n当前项目：%s\n项目根目录：%s（write_file 与 run_command 只能作用在这个目录内）\n",
			r.ProjectName, r.ProjectPath)
	} else {
		b.WriteString("\n\n当前没有选择项目：write_file 和 run_command 暂不可用。用户想让你写东西时，先提醒他创建或选择一个项目（侧边栏「项目」区）。\n")
	}
	if len(r.ReadDirs) > 0 {
		b.WriteString("\n以下目录已授权只读（可查看代码，不可修改）：\n")
		for _, d := range r.ReadDirs {
			b.WriteString("- " + d + "\n")
		}
	}

	// 5) 补充指令
	if ins := strings.TrimSpace(r.Instructions); ins != "" {
		b.WriteString("\n\n" + ins)
	}
	return b.String()
}

// PlanGuideFor 返回工作模式的补充指令（作为 Instructions 传入）。
//
// 设计对齐主流开源 Agent 的 plan mode 共识（Cline / Roo Code / Claude Code）：
//   - plan 模式 = 只读工具 + 强制先调研后结构化计划 + 禁止动手改
//   - build 模式 = 正常执行，按已确认的计划分步实施
func PlanGuideFor(planMode bool) string {
	if planMode {
		return `【当前为计划模式（Plan Mode）】
你现在处于「先计划、后开发」的计划模式，规则：
1. 只允许调研：用 list_files / read_file / search_files（及只读 MCP 工具）查看代码与文档；禁止修改文件、禁止执行命令（这两类工具已对你会话禁用）。
2. 调研充分后，输出一份结构化实施计划，必须包含：
   - 目标：一句话说明要做什么
   - 现状调研：发现了什么（引用具体文件/函数）
   - 实施步骤：按 1. 2. 3. 编号，每步写清改哪个文件、做什么（只写步骤要点，不要把大段完整代码贴进计划）
   - 风险与边界：可能踩的坑、明确不做的事
3. 计划输出后等待用户在界面上点「批准并开始开发」；不要自己尝试写文件或执行命令。
   用户若提出修改意见，按意见更新计划后继续等待批准。
4. 不要提前动手实现；也不要在计划里粘贴大段代码或命令让用户手动执行。`
	}
	return `【当前为开发模式（Build Mode）】
你处于正常开发模式，拥有完整工具（读写文件、执行白名单命令）。
- 若本会话已有用户确认过的计划，按计划分步实施，每完成一步简要汇报。
- 动手前仍应先读相关文件核实现状，不要凭空猜。`
}
