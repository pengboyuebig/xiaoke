package agents

import (
	"errors"
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

// 内置人格：种子标记 builtin，不可编辑、删除、切换工具；客户端不能自封 builtin。
func TestBuiltinAgentIsProtected(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDefault(Agent{Name: "小柯", Avatar: "/assets/avatar.png", Tools: true}); err != nil {
		t.Fatal(err)
	}
	builtin := s.List()[0]
	if !builtin.Builtin {
		t.Fatal("种子人格应标记为内置")
	}
	// 内置人格不可修改
	builtin.Description = "试图修改"
	if _, err := s.Update(builtin.ID, builtin); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("修改内置人格应返回 ErrBuiltin，got %v", err)
	}
	// 内置人格不可删除（即使只剩它一个也应先报内置错误）
	if err := s.Delete(builtin.ID); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("删除内置人格应返回 ErrBuiltin，got %v", err)
	}
	// 客户端不能通过 Create 自封 builtin
	created, err := s.Create(Agent{Name: "冒充者", Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Builtin {
		t.Fatal("Create 不应接受客户端传入的 builtin")
	}
	// 普通智能体可以正常删除
	if err := s.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	// 旧数据：已存在同名小柯但没有 builtin 标记，EnsureDefault 应补标记而不是新增
	s2, _ := Open(filepath.Join(t.TempDir(), "old.json"))
	old, err := s2.Create(Agent{Name: "小柯"})
	if err != nil {
		t.Fatal(err)
	}
	if old.Builtin {
		t.Fatal("普通创建不应是内置")
	}
	if err := s2.EnsureDefault(Agent{Name: "小柯"}); err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 1 {
		t.Fatalf("同名旧人格应被标记为内置而不是新增，got %d", len(list))
	}
	if !list[0].Builtin {
		t.Fatal("同名旧人格应被补上内置标记")
	}
}

// 重名拦截：Create 与 Update 都不允许与其他智能体同名（改名排除自己）。
func TestDuplicateName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDefault(Agent{Name: "小柯"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Agent{Name: "小柯"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("与内置重名应被拦截，got %v", err)
	}
	a, err := s.Create(Agent{Name: "分析师"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Agent{Name: "分析师"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("重复创建应被拦截，got %v", err)
	}
	// 改名撞别人
	if _, err := s.Update(a.ID, Agent{Name: "小柯", Tools: true}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("改名撞名应被拦截，got %v", err)
	}
	// 改成自己的名字（排除自己）应允许
	same, err := s.Update(a.ID, Agent{Name: "分析师", Description: "只改描述"})
	if err != nil {
		t.Fatalf("保留自己名字应允许: %v", err)
	}
	if same.Description != "只改描述" {
		t.Fatalf("更新未生效: %+v", same)
	}
}
