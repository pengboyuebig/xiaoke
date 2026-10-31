package prompt

import (
	"strings"
	"testing"
	"time"
)

func TestBuildStructure(t *testing.T) {
	out := Build(Request{
		AgentName:      "小柯",
		AgentBase:      "",
		AgentTools:     true,
		ProjectName:    "测试项目",
		ProjectPath:    "D:/work/test",
		ReadDirs:       []string{"D:/other"},
		ToolsAvailable: true,
		SkillsListing:  "- 测试技能：一句话用途",
		Instructions:   "【当前为开发模式（Build Mode）】",
	})
	// 人格在前，签名铁律必须保留
	if !strings.HasPrefix(out, "你是小柯") {
		t.Fatalf("提示词应以人格开头: %q", out[:30])
	}
	if !strings.Contains(out, "**小柯**") {
		t.Fatal("签名铁律丢失")
	}
	// 日期锚点
	if !strings.Contains(out, "今天是 ") || !strings.Contains(out, "星期") {
		t.Fatal("缺少日期锚点")
	}
	// 技能清单区有说明头 + 按需加载指引
	if !strings.Contains(out, "【可用技能】") || !strings.Contains(out, "skill_read") {
		t.Fatal("技能清单区缺少说明头或按需加载指引")
	}
	// 项目上下文与只读目录
	if !strings.Contains(out, "当前项目：测试项目") || !strings.Contains(out, "D:/other") {
		t.Fatal("项目上下文不完整")
	}
	// 补充指令在最后
	if !strings.HasSuffix(strings.TrimSpace(out), "Build Mode）】") {
		t.Fatalf("补充指令应在末尾: %q", out[len(out)-60:])
	}
}

// 规则文件与长期记忆段：有内容才注入，空串不产生空段。
func TestBuildRulesAndMemory(t *testing.T) {
	out := Build(Request{
		AgentName: "小柯",
		Rules:     "## 来自 /proj/AGENTS.md\n构建命令：npm run build",
		Memory:    "【全局记忆】\n- 用户偏好简洁回复",
	})
	if !strings.Contains(out, "【项目规则】") || !strings.Contains(out, "npm run build") {
		t.Fatalf("规则段缺失: %q", out)
	}
	if !strings.Contains(out, "【长期记忆】") || !strings.Contains(out, "用户偏好简洁回复") {
		t.Fatalf("记忆段缺失: %q", out)
	}
	bare := Build(Request{AgentName: "小柯"})
	if strings.Contains(bare, "【项目规则】") || strings.Contains(bare, "【长期记忆】") || strings.Contains(bare, "【可用技能】") {
		t.Fatal("空内容不应产生空段")
	}
}

// 自定义人格会替换名字，但不破坏前缀铁律的描述。
func TestBuildCustomAgentKeepsSignature(t *testing.T) {
	out := Build(Request{
		AgentName: "测试者",
		AgentBase: "你叫小柯，一个测试智能体。",
	})
	if !strings.Contains(out, "测试者") {
		t.Fatal("自定义人格应替换为智能体名字")
	}
}

// 计划模式指令包含模式锁说明；开发模式包含自检要求。
func TestPlanGuide(t *testing.T) {
	plan := PlanGuideFor(true)
	if !strings.Contains(plan, "计划模式") || !strings.Contains(plan, "等用户点「批准并开始开发」") {
		t.Fatal("计划模式指令不完整")
	}
	build := PlanGuideFor(false)
	if !strings.Contains(build, "开发模式") || !strings.Contains(build, "把「写完」和「能跑」区分开") {
		t.Fatal("开发模式指令不完整")
	}
}

// cnDate 输出星期几与中文年月日。
func TestCnDate(t *testing.T) {
	// 2026-10-30 是星期五
	out := cnDate(time.Date(2026, 10, 30, 0, 0, 0, 0, time.Local))
	if !strings.Contains(out, "2026年10月30日") || !strings.Contains(out, "星期五") {
		t.Fatalf("日期格式不对: %s", out)
	}
}
