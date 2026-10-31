// Package prompt 组装系统提示词：人设 + 智能体人格 + 工具规范 + 技能 + 项目上下文。
package prompt

import (
	"fmt"
	"strings"
	"time"

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
	SkillsListing  string   // 启用技能的清单（正文由模型用 skill_read 按需读取）
	Rules          string   // 项目规则文件（AGENTS.md / CLAUDE.md 等，已转义）
	Memory         string   // 长期记忆（全局 + 项目两层，budget 内）
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

	// 当前日期：模型的时间感会漂移，涉及“最近/今天”的任务需要锚点
	b.WriteString("\n\n今天是 " + cnDate(time.Now()) + "。")

	// 2) 工具规范：智能体声明用工具 或 会话确实有工具时注入
	if r.AgentTools || r.ToolsAvailable {
		b.WriteString(persona.ToolGuide)
		if !r.AgentTools && r.ToolsAvailable {
			b.WriteString("\n（当前智能体未声明使用工具，但本会话工具可用，按需调用。）")
		}
	}

	// 3) 技能清单：只注入「有什么可用」，正文按需读取——常驻成本是 KB 级，
	// 正文常驻（旧行为）会在技能多起来后挤占任务上下文。
	if s := strings.TrimSpace(r.SkillsListing); s != "" {
		b.WriteString("\n\n【可用技能】以下技能与当前任务相关时，先用 skill_read 读取其完整方法论再动手；无关时不要读也不要理会：")
		b.WriteString(s)
	}

	// 3.5) 项目规则文件：仓库约定跟着项目走（构建命令、风格、结构）。
	// 注入顺序浅→深；优先级声明：用户当前指令 > 深层规则文件 > 浅层规则文件。
	if rules := strings.TrimSpace(r.Rules); rules != "" {
		b.WriteString("\n\n【项目规则】以下是项目里的规则文件（AGENTS.md / CLAUDE.md 等，浅层在前、深层在后），是对你如何在这个项目工作的约定，与当前任务相关时必须遵守；深层规则比浅层规则优先，用户当前对话里的明确指令优先于一切规则文件：")
		b.WriteString("\n" + rules)
	}

	// 3.6) 长期记忆：跨会话保留的用户偏好/项目知识；模型可用 memory_append 增补。
	if mem := strings.TrimSpace(r.Memory); mem != "" {
		b.WriteString("\n\n【长期记忆】以下是往期会话沉淀的记忆，相关时直接运用；已有内容不要向用户复述确认，也不要重复写入：")
		b.WriteString("\n" + mem)
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

// cnDate 返回「2026年10月30日 星期五」格式的日期锚点。
func cnDate(t time.Time) string {
	week := []string{"日", "一", "二", "三", "四", "五", "六"}[int(t.Weekday())]
	return fmt.Sprintf("%d年%d月%d日 星期%s", t.Year(), int(t.Month()), t.Day(), week)
}

// PlanGuideFor 返回工作模式的补充指令（作为 Instructions 传入）。
//
// 设计对齐主流开源 Agent 的 plan mode 共识（Cline / Roo Code / Claude Code）：
//   - plan 模式 = 只读工具 + 强制先调研后结构化计划 + 禁止动手改
//   - build 模式 = 正常执行，按已确认的计划分步实施
func PlanGuideFor(planMode bool) string {
	if planMode {
		return `【当前为计划模式（Plan Mode）】
你处于「先计划、后开发」的计划模式，规则：
1. 只允许调研：用 list_files / read_file / search_files（及只读 MCP 工具、explore 子代理）查看代码与文档；写文件与执行命令这两类工具已对你会话禁用，不要尝试也不要向用户索要权限。
2. 调研充分后，输出一份结构化实施计划，必须包含：
   - 目标：一句话说明要做什么
   - 现状调研：发现了什么（引用具体文件/函数/行号，写你确认过的，不写猜的）
   - 实施步骤：按 1. 2. 3. 编号，每步写清改哪个文件、做什么（只写步骤要点，不贴大段完整代码）
   - 风险与边界：可能踩的坑、明确不做的事
3. 计划输出后停在那里，等用户点「批准并开始开发」；用户提修改意见就更新计划，继续等批准。催你动手也不行——模式锁着，这是机制不是态度。
4. 计划里不要塞大段代码或让用户手动执行的命令；那是开发模式的事。`
	}
	return `【当前为开发模式（Build Mode）】
你拥有完整工具（读写文件、执行白名单命令）。
- 若本会话已有用户确认过的计划，按计划分步实施：每完成一步用一句话汇报改了什么，再进入下一步；发现计划与现实不符时，先停下来说明偏差，不要擅自改道。
- 动手前先读相关文件核实现状；「我记得这里是这样的」不算核实。
- 每个文件写完做一次语法/构建层面的自检（能 build 就 build），把「写完」和「能跑」区分开。`
}
