package agents

import (
	"path/filepath"
	"testing"
)

func TestSeedAndCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 首次无数据
	if len(s.List()) != 0 {
		t.Fatal("新仓库应为空")
	}
	// 种子默认智能体
	if err := s.EnsureDefault(Agent{Name: "小柯", Avatar: "/assets/avatar.png", SystemPrompt: "你是小柯", Tools: true}); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("种子后应有 1 个，got %d", len(s.List()))
	}
	// 再种子不应重复
	if err := s.EnsureDefault(Agent{Name: "小柯"}); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("重复种子不应新增")
	}

	// 创建
	a, err := s.Create(Agent{Name: "写作助手", Description: "写文章", Avatar: "🐱", SystemPrompt: "你是写作助手"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 2 {
		t.Fatal("创建后应有 2 个")
	}
	// 非法名
	if _, err := s.Create(Agent{Name: "bad/name"}); err == nil {
		t.Fatal("非法名应被拒绝")
	}
	// 非法头像
	if _, err := s.Create(Agent{Name: "x2", Avatar: "../../../etc/passwd"}); err == nil {
		t.Fatal("非法头像路径应被拒绝")
	}
	// 更新
	a.Description = "改了"
	up, err := s.Update(a.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if up.Description != "改了" {
		t.Fatal("更新未生效")
	}
	// 删除（至少保留一个）
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("删除后应剩 1 个")
	}
	if err := s.Delete(s.List()[0].ID); err == nil {
		t.Fatal("最后一个智能体不应可删")
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	s, _ := Open(path)
	if err := s.EnsureDefault(Agent{Name: "小柯"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Agent{Name: "二号", Avatar: "🦊"}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 2 {
		t.Fatalf("重开应有 2 个，got %d", len(s2.List()))
	}
}
