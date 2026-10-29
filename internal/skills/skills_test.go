package skills

import (
	"path/filepath"
	"strings"
	"testing"
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
