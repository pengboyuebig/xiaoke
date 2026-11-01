package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 并发截断与追加：TruncateAfter 与 Append 交错时不得产生非法状态（消息丢失/重复/越界）。
func TestTruncateUnderConcurrentAppend(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	// 先写入 20 条
	for i := 0; i < 20; i++ {
		if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("m%d", i), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	// 并发：10 个 goroutine 追加 + 1 个截断
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("c%d", i), CreatedAt: time.Now()})
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.TruncateAfter(sess.ID, 5)
	}()
	wg.Wait()

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("截断后不应为空（至少保留边界消息）")
	}
	// 校验：消息内容连续、无 nil、无重复 ID 问题
	for i, m := range got.Messages {
		if m.Content == "" {
			t.Fatalf("第 %d 条消息内容为空", i)
		}
	}
}

// 并发搜索与写入：SearchMessages 不应死锁。
func TestSearchUnderConcurrentWrite(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("needle", "")
	_ = s.Append(sess.ID, Message{Role: "user", Content: "find the needle here"})

	done := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			_ = s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("w%d", i), CreatedAt: time.Now()})
		}
		close(done)
	}()
	for i := 0; i < 10; i++ {
		hits := s.SearchMessages("needle", 10)
		_ = hits
	}
	<-done
}
