package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/config"
	"xiaoke/internal/memory"
	"xiaoke/internal/store"
	"xiaoke/internal/tools"
)

// mustTestStore / mustTestMemory 是 dream 测试的构造辅助（Handler 零值 + 依赖注入）。
func mustTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mustTestMemory(t *testing.T) *memory.Store {
	t.Helper()
	m, err := memory.Open(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// dreamTranscript：取尾部消息、跳过隐藏消息与工具结果、按预算截断。
func TestDreamTranscript(t *testing.T) {
	h := &Handler{store: mustTestStore(t)}
	// 造 4 个会话（≥ dreamMaxSessions 之下即可）
	for i := 0; i < 3; i++ {
		sess, _ := h.store.Create("会话"+string(rune('A'+i)), "")
		for j := 0; j < 20; j++ {
			_ = h.store.Append(sess.ID, store.Message{Role: "user", Content: "问题内容一些文字", CreatedAt: time.Now()})
			_ = h.store.Append(sess.ID, store.Message{Role: "assistant", Content: "回答内容一些文字", CreatedAt: time.Now()})
			_ = h.store.Append(sess.ID, store.Message{Role: "tool", Content: "工具结果不该出现", CreatedAt: time.Now()})
		}
		_ = h.store.Append(sess.ID, store.Message{Role: "user", Content: "批准计划的隐藏指令", Hidden: true, CreatedAt: time.Now()})
	}
	metas, _ := h.store.List()
	out, checked := h.dreamTranscript(metas)
	if checked == 0 {
		t.Fatal("应至少检查 1 个会话")
	}
	if strings.Contains(out, "工具结果不该出现") {
		t.Fatal("工具结果不应进入蒸馏输入")
	}
	if strings.Contains(out, "批准计划的隐藏指令") {
		t.Fatal("隐藏消息不应进入蒸馏输入")
	}
	if len(out) > dreamTranscriptLimit {
		t.Fatalf("转录超预算: %d", len(out))
	}
}

// dreamParseItems：剥 bullet、丢（无）、上限截断。
func TestDreamParseItems(t *testing.T) {
	out := "- 用户偏好简洁回复\n\n- 项目的构建命令是 npm run build\n（无）"
	items := dreamParseItems(out)
	if len(items) != 2 || items[0] != "用户偏好简洁回复" {
		t.Fatalf("解析不符: %v", items)
	}
	if got := dreamParseItems("（无）"); got != nil {
		t.Fatalf("（无）应返回空: %v", got)
	}
	many := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, "- 条目")
	}
	if got := dreamParseItems(strings.Join(many, "\n")); len(got) != dreamMaxItems {
		t.Fatalf("应截到 %d 条: %d", dreamMaxItems, len(got))
	}
}

// dreamOnce 端到端：假 streamFn 输出条目 → ImportGlobal 去重落库。
func TestDreamOnceImports(t *testing.T) {
	h := &Handler{
		store:  mustTestStore(t),
		memory: mustTestMemory(t),
		set:    nil, // dreamOnce(带 streamFn) 不走 Effective 分支
	}
	// 造 3 个会话过「会话数门」
	for i := 0; i < 3; i++ {
		sess, _ := h.store.Create("历史会话", "")
		_ = h.store.Append(sess.ID, store.Message{
			Role: "user", Content: "我偏好把测试文件放 tests/ 目录，请记住这一点", CreatedAt: time.Now(),
		})
		_ = h.store.Append(sess.ID, store.Message{
			Role: "assistant", Content: "**小柯** 明白，已记住你的目录偏好。", CreatedAt: time.Now(),
		})
	}
	replies := 0
	fake := func(ctx context.Context, cfg config.LLMConfig, msgs []ChatMessage, defs []tools.ToolDef, hooks Hooks, reasoning string) (string, []ToolCall, error) {
		replies++
		return "- 用户偏好把测试文件放 tests/ 目录\n- 用户希望回答保持简洁", nil, nil
	}
	h.dreamOnce(fake)
	if replies != 1 {
		t.Fatalf("整理应调用一次模型: %d", replies)
	}
	got, err := h.memory.Read(memory.ScopeGlobal, "")
	if err != nil || !strings.Contains(got, "tests/") {
		t.Fatalf("记忆应被导入: %v %q", err, got)
	}
	// 再跑一次：去重后不新增
	h.dreamOnce(fake)
	got2, _ := h.memory.Read(memory.ScopeGlobal, "")
	if strings.Count(got2, "tests/") != 1 {
		t.Fatalf("重复整理不应产生重复条目: %q", got2)
	}
}
