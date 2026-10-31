package skills

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSeedCreateToggleAssembly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 种子
	if err := s.EnsureSeed(Skill{Name: "示例", Description: "d", Content: "内容A", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("种子失败")
	}
	// 种子不重复
	if err := s.EnsureSeed(Skill{Name: "示例", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("重复种子")
	}

	// 创建
	sk, err := s.Create(Skill{Name: "代码评审", Description: "评审规范", Content: "- 看边界条件\n- 看命名", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// 同名拒绝
	if _, err := s.Create(Skill{Name: "代码评审", Content: "x"}); err == nil {
		t.Fatal("同名应拒绝")
	}
	// 空内容拒绝
	if _, err := s.Create(Skill{Name: "空技能", Content: "  "}); err == nil {
		t.Fatal("空内容应拒绝")
	}

	// 装配：两条启用内容都应出现
	asm := s.Assembly()
	if !strings.Contains(asm, "内容A") || !strings.Contains(asm, "代码评审") || !strings.Contains(asm, "看边界条件") {
		t.Fatalf("装配缺少内容: %q", asm)
	}
	// 停用后装配不包含
	if _, err := s.Toggle(sk.ID); err != nil {
		t.Fatal(err)
	}
	asm2 := s.Assembly()
	if strings.Contains(asm2, "看边界条件") {
		t.Fatal("停用后不应注入")
	}
	// 再启用
	if _, err := s.Toggle(sk.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Assembly(), "看边界条件") {
		t.Fatal("重新启用应注入")
	}

	// 更新 + 删除
	sk.Description = "改"
	if _, err := s.Update(sk.ID, sk); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sk.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("删除后应剩 1 条")
	}
}

// 技能注入总量超上限时：按顺序装载、截断并给出提示，而不是无限膨胀系统提示词。
func TestAssemblyByteCap(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("技能内容。", 3000) // ~36KB
	for i := 0; i < 3; i++ {
		if _, err := s.Create(Skill{
			Name: "技能" + string(rune('A'+i)), Content: big, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	out := s.Assembly()
	if len(out) <= maxAssemblyBytes {
		t.Fatalf("应发生截断（超过上限），len=%d", len(out))
	}
	if !strings.Contains(out, "超出注入上限") || !strings.Contains(out, "只装入了部分") {
		t.Fatal("截断应有明确标注")
	}
	// 截断点不应切碎 UTF-8
	if !utf8.ValidString(out) {
		t.Fatal("截断产生了非法 UTF-8")
	}
}

// Assembly 缓存：写操作后立即失效，下一次读取反映最新状态。
func TestAssemblyCacheInvalidation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	sk, err := s.Create(Skill{Name: "缓存技能", Content: "第一次内容", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out := s.Assembly(); !strings.Contains(out, "第一次内容") {
		t.Fatalf("首次装配缺少内容: %q", out[:50])
	}
	if _, err := s.Update(sk.ID, Skill{Name: "缓存技能", Content: "第二次内容", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if out := s.Assembly(); !strings.Contains(out, "第二次内容") || strings.Contains(out, "第一次内容") {
		t.Fatal("Update 后缓存未失效")
	}
	if _, err := s.Toggle(sk.ID); err != nil {
		t.Fatal(err)
	}
	if out := s.Assembly(); strings.Contains(out, "第二次内容") {
		t.Fatal("Toggle 后缓存未失效")
	}
	if err := s.Delete(sk.ID); err != nil {
		t.Fatal(err)
	}
	if out := s.Assembly(); out != "" {
		t.Fatalf("Delete 后装配应为空: %q", out)
	}
}

// 按需加载：清单只含名字+描述（不含正文），ContentByName 只给启用技能的正文。
func TestListingAndContentByName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Skill{Name: "技能甲", Description: "结构化表达", Content: "甲的完整方法论正文", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.Create(Skill{Name: "技能乙", Description: "已停用", Content: "乙的正文", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Toggle(disabled.ID); err != nil {
		t.Fatal(err)
	}

	listing := s.Listing()
	if !strings.Contains(listing, "技能甲") || !strings.Contains(listing, "结构化表达") {
		t.Fatalf("清单应含启用的技能: %q", listing)
	}
	if strings.Contains(listing, "甲的完整方法论正文") {
		t.Fatal("清单不应包含正文（正文按需加载）")
	}
	if strings.Contains(listing, "技能乙") {
		t.Fatal("停用技能不应出现在清单")
	}

	// 读取正文
	body, err := s.ContentByName("技能甲")
	if err != nil || !strings.Contains(body, "甲的完整方法论") {
		t.Fatalf("读取正文失败: %v %q", err, body)
	}
	// 禁用读不到
	if _, err := s.ContentByName("技能乙"); err == nil {
		t.Fatal("禁用技能应读不到")
	}
	// 不存在时报错并列出可用技能名
	_, err = s.ContentByName("不存在的技能")
	if err == nil || !strings.Contains(err.Error(), "技能甲") {
		t.Fatalf("报错应列出可用技能: %v", err)
	}
	// 空仓库
	empty, _ := Open(filepath.Join(t.TempDir(), "empty.json"))
	if out := empty.Listing(); out != "" {
		t.Fatalf("无技能时清单应为空: %q", out)
	}
}
