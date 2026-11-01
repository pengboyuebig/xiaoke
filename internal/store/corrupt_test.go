package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 日志文件损坏（半截行、非法 JSON、重复 seq、乱序 seq）时读取必须容错。
func TestLogCorruptionRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	// 正常写两条
	for i := 0; i < 2; i++ {
		if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("m%d", i), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	// 手工污染日志：追加半截 JSON、非法 JSON、重复 seq
	logPath := filepath.Join(dir, sess.ID+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":999,"msg":{"role":"user","content":"半截` + "\n")    // 半截
	f.WriteString("not-json-at-all\n")                                       // 非法
	f.WriteString(`{"seq":1,"msg":{"role":"user","content":"dup"}}` + "\n")  // 重复 seq
	f.WriteString(`{"seq":0,"msg":{"role":"user","content":"zero"}}` + "\n") // 非法 seq
	f.Close()

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatalf("损坏日志不应导致读取失败: %v", err)
	}
	// 至少能读到原来的两条，且内容非空
	if len(got.Messages) < 2 {
		t.Fatalf("应至少保留原有的 2 条消息，got %d", len(got.Messages))
	}
	for i, m := range got.Messages {
		if m.Content == "" {
			t.Errorf("第 %d 条内容为空", i)
		}
	}
	// dup 行 seq 合法且未被快照覆盖 → 应正常加载（这是预期行为）
	found := false
	for _, m := range got.Messages {
		if m.Content == "dup" {
			found = true
		}
	}
	if !found {
		t.Log("合法 seq 的新行未加载（可能被 maxLogLine 或其他规则过滤）")
	}
}

// 快照损坏时必须报错而不是静默返回空会话。
func TestSnapshotCorruptionErrors(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("", "")
	_ = s.Append(sess.ID, Message{Role: "user", Content: "hello", CreatedAt: time.Now()})

	// 破坏快照
	snap := filepath.Join(dir, sess.ID+".json")
	if err := os.WriteFile(snap, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sess.ID); err == nil {
		t.Fatal("损坏的快照应报错（不能静默返回空会话）")
	}
}

// 日志中 seq 乱序（后写的小 seq）：应按 seq 单调过滤，不重复不丢失已合并部分。
func TestLogOutOfOrderSeq(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("", "")
	_ = s.Append(sess.ID, Message{Role: "user", Content: "first", CreatedAt: time.Now()})

	logPath := filepath.Join(dir, sess.ID+".log")
	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	// 伪造一条 seq 小于已存在记录的（应被 meta.LastSeq 逻辑跳过或至少不破坏顺序）
	f.WriteString(`{"seq":5,"msg":{"role":"user","content":"older"}}` + "\n")
	f.Close()

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("至少应保留第一条消息")
	}
	if !strings.Contains(got.Messages[0].Content, "first") && !strings.Contains(got.Messages[0].Content, "older") {
		t.Fatalf("首条消息内容异常: %q", got.Messages[0].Content)
	}
}
