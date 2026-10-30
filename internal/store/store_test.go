package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTryLockSession(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	unlock, ok := s.TryLockSession(sess.ID)
	if !ok {
		t.Fatal("首次加锁应成功")
	}
	if _, ok := s.TryLockSession(sess.ID); ok {
		t.Fatal("同一会话第二次加锁应失败")
	}
	unlock()
	unlock2, ok := s.TryLockSession(sess.ID)
	if !ok {
		t.Fatal("解锁后应能再次加锁")
	}
	unlock2()
}

func TestTryLockSessionIgnoresInvalidID(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "not-a-valid-id"} {
		unlock, ok := s.TryLockSession(id)
		if !ok {
			t.Fatalf("非法/空 ID 不应参与锁: %q", id)
		}
		unlock()
	}
}

func TestAppendAndGet(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sess.ID, Message{Role: "user", Content: "帮我写个脚本"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "帮我写个脚本" {
		t.Fatalf("消息落库异常: %+v", got.Messages)
	}
	if got.Title == "" {
		t.Fatal("首条用户消息应作为会话标题")
	}
	// 追加只写日志，快照仍是空的（证明没有每条消息重写整个会话）
	snap, err := s.loadSnapshot(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 0 {
		t.Fatalf("追加消息不应重写快照，实际快照已有 %d 条", len(snap.Messages))
	}
}

func TestListUsesMetaAndFallback(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sess.ID, Message{Role: "user", Content: "hello world"}); err != nil {
		t.Fatal(err)
	}
	metas, err := s.List()
	if err != nil || len(metas) != 1 {
		t.Fatalf("List 结果异常: %+v %v", metas, err)
	}
	if metas[0].MessageCount != 1 || metas[0].Title == "" {
		t.Fatalf("meta 摘要异常: %+v", metas[0])
	}
	if _, err := os.Stat(s.metaPath(sess.ID)); err != nil {
		t.Fatalf("meta 文件应存在: %v", err)
	}

	// 删掉 meta 后，List 应回退到完整加载，仍然能看到会话
	if err := os.Remove(s.metaPath(sess.ID)); err != nil {
		t.Fatal(err)
	}
	metas, err = s.List()
	if err != nil || len(metas) != 1 || metas[0].MessageCount != 1 {
		t.Fatalf("缺少 meta 时应回退加载: %+v %v", metas, err)
	}
}

func TestDeleteRemovesAllFiles(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sess.ID, Message{Role: "user", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.path(sess.ID), s.logPath(sess.ID), s.metaPath(sess.ID)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("删除后文件仍存在: %s (%v)", p, err)
		}
	}
}

func TestLogCompaction(t *testing.T) {
	old := logCompactBytes
	logCompactBytes = 1
	defer func() { logCompactBytes = old }()

	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("合并后消息数异常: %d", len(got.Messages))
	}
	// 日志已被合并并删除
	if _, err := os.Stat(s.logPath(sess.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("合并后日志应被删除: %v", err)
	}
	snap, err := s.loadSnapshot(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 3 || snap.LastSeq == 0 {
		t.Fatalf("快照未包含合并后的消息: %+v", snap)
	}
}

func TestLegacySessionWithoutMeta(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef"
	legacy := Session{
		ID: id, Title: "旧会话", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Messages: []Message{{Role: "user", Content: "旧消息"}},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(id)
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("旧会话读取异常: %+v %v", got, err)
	}
	metas, err := s.List()
	if err != nil || len(metas) != 1 || metas[0].MessageCount != 1 {
		t.Fatalf("旧会话列表异常: %+v %v", metas, err)
	}
	// 旧会话也能继续追加
	if err := s.Append(id, Message{Role: "user", Content: "新消息"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(id)
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("旧会话追加异常: %+v %v", got, err)
	}
}

func TestConcurrentAppends(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("m%d", i)}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != n {
		t.Fatalf("并发追加后消息数应为 %d，实际 %d", n, len(got.Messages))
	}
	metas, err := s.List()
	if err != nil || len(metas) != 1 || metas[0].MessageCount != n {
		t.Fatalf("并发追加后 meta 异常: %+v %v", metas, err)
	}
}
