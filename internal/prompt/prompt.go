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
