package store

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestDeleteKeepsTrashCopy(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("我的会话", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sess.ID, Message{Role: "user", Content: "重要内容"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.TrashDir())
	if err != nil {
		t.Fatalf("回收站目录应存在: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("回收站应有 1 个目录，实际 %d", len(entries))
	}
	trashID := entries[0].Name()
	if !strings.HasPrefix(trashID, sess.ID+"-") {
		t.Fatalf("回收站目录名应含会话 ID: %s", trashID)
	}
	// 快照副本应能完整读回消息
	data, err := os.ReadFile(filepath.Join(s.TrashDir(), trashID, sess.ID+".json"))
	if err != nil {
		t.Fatalf("回收站快照应存在: %v", err)
	}
	var snap Session
	if err := json.Unmarshal(data, &snap); err != nil || snap.ID != sess.ID {
		t.Fatalf("回收站快照异常: %v", err)
	}
}

// 回收站：列表能看到标题/消息数，恢复后会话回来且消息完整，同 ID 已存在时拒绝覆盖。
func TestTrashListAndRestore(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("要恢复的会话", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListTrash()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("回收站应有 1 条，实际 %d", len(items))
	}
	if items[0].ID != sess.ID || items[0].Title != "要恢复的会话" || items[0].MessageCount != 2 {
		t.Fatalf("回收站条目信息不对: %+v", items[0])
	}
	if items[0].DeletedAt.IsZero() || items[0].SizeBytes <= 0 {
		t.Fatalf("回收站条目缺少时间/大小: %+v", items[0])
	}

	id, err := s.RestoreTrash(items[0].Dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != sess.ID {
		t.Fatalf("恢复的 ID 不对: %s", id)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatalf("恢复后应能读取会话: %v", err)
	}
	if len(got.Messages) != 2 || got.Title != "要恢复的会话" {
		t.Fatalf("恢复后内容不完整: %+v", got)
	}
	// 恢复成功会移除回收站目录
	if left, _ := s.ListTrash(); len(left) != 0 {
		t.Fatalf("恢复后回收站应为空，实际 %d", len(left))
	}

	// 会话仍在时，手工造一个同名回收站目录，验证冲突分支
	dup := filepath.Join(s.TrashDir(), sess.ID+"-20200101-000000")
	if err := os.MkdirAll(dup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(s.path(sess.ID), filepath.Join(dup, sess.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreTrash(filepath.Base(dup)); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("会话仍存在时应返回 ErrSessionExists，got %v", err)
	}
}

// 回收站清理：只删超过保留期的记录，且 d<=0 时什么都不删。
func TestTrashPurge(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("过期会话", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListTrash()
	if err != nil || len(items) != 1 {
		t.Fatalf("应有 1 条回收站记录: %v", err)
	}
	// d<=0 不清理
	if n, err := s.PurgeTrashOlderThan(0); err != nil || n != 0 {
		t.Fatalf("d<=0 不应清理，got n=%d err=%v", n, err)
	}
	// 保留期很长，刚删的不应被清
	if n, err := s.PurgeTrashOlderThan(365 * 24 * time.Hour); err != nil || n != 0 {
		t.Fatalf("未过期记录不应清理，got n=%d err=%v", n, err)
	}
	// 把目录时间戳改成很久以前，再按 1 天清理
	old := filepath.Join(s.TrashDir(), sess.ID+"-20200101-000000")
	if err := os.Rename(filepath.Join(s.TrashDir(), items[0].Dir), old); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeTrashOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应清理 1 条过期记录，got %d", n)
	}
	if left, _ := s.ListTrash(); len(left) != 0 {
		t.Fatalf("清理后应为空，实际 %d", len(left))
	}
	// 非法目录名
	if err := s.PurgeTrash("not-a-valid-name"); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("非法目录名应返回 ErrTrashNotFound，got %v", err)
	}
}

func TestExportContainsLogMessages(t *testing.T) {
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
	data, err := s.Export(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("导出 JSON 不可解析: %v", err)
	}
	if got.ID != sess.ID || len(got.Messages) != 3 {
		t.Fatalf("导出内容不完整: %+v", got)
	}
}

func TestBackupAndPrune(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("备份会话", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(sess.ID, Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := s.Backup(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("备份不是合法 zip: %v", err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"manifest.json", sess.ID + ".json", sess.ID + ".meta.json"} {
		if !names[want] {
			t.Fatalf("备份缺少 %s，实际: %v", want, names)
		}
	}

	// 保留策略：只留最近 2 份
	for i := 0; i < 4; i++ {
		if _, err := s.Backup(dir, 2); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	zips := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".zip") {
			zips++
		}
	}
	if zips > 2 {
		t.Fatalf("备份保留策略失效，剩余 %d 份", zips)
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

// 上下文摘要：SetSummary 持久化后，重新加载与会话列表都能看到；
// 再追加消息不影响摘要边界。
func TestSummaryPersistence(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("摘要会话", "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 4; i++ {
		if err := s.Append(sess.ID, Message{Role: "user", Content: fmt.Sprintf("q%d", i), CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	upTo := base.Add(2 * time.Minute)
	if err := s.SetSummary(sess.ID, "前两轮：用户问了 q0、q1 并得到回答。", upTo); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary == "" || !got.SummaryUpTo.Equal(upTo) {
		t.Fatalf("摘要未还原: summary=%q upTo=%v", got.Summary, got.SummaryUpTo)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("消息不应丢失，got %d", len(got.Messages))
	}
	// 摘要之后再追加消息，边界不被破坏
	if err := s.Append(sess.ID, Message{Role: "user", Content: "q4", CreatedAt: base.Add(5 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(sess.ID)
	if got2.Summary == "" || len(got2.Messages) != 5 {
		t.Fatalf("追加后摘要应保留: summary=%q msgs=%d", got2.Summary, len(got2.Messages))
	}
}

// 待办清单：整表替换 + 持久化还原 + 日志收敛后不丢消息。
func TestSetTodosPersistence(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("待办会话", "")
	if err := s.Append(sess.ID, Message{Role: "user", Content: "做一个功能", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	todos := []Todo{
		{Content: "调研现状", Status: "completed", UpdatedAt: time.Now()},
		{Content: "写实现", Status: "in_progress", UpdatedAt: time.Now()},
		{Content: "补测试", Status: "pending", UpdatedAt: time.Now()},
	}
	if err := s.SetTodos(sess.ID, todos); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Todos) != 3 || got.Todos[1].Status != "in_progress" {
		t.Fatalf("待办未还原: %+v", got.Todos)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("消息不应丢失: %d", len(got.Messages))
	}
	// 第二次替换（模拟 todo 更新）
	todos[1].Status = "completed"
	if err := s.SetTodos(sess.ID, todos); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(sess.ID)
	if got2.Todos[1].Status != "completed" {
		t.Fatalf("待办更新未生效: %+v", got2.Todos)
	}
	// 非法 ID 拒绝
	if err := s.SetTodos("nope", nil); err == nil {
		t.Fatal("非法 ID 应报错")
	}
}

// 用量累计：多次 AddUsage 累加且重启可还原。
func TestAddUsageAccumulates(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("", "")
	if err := s.Append(sess.ID, Message{Role: "user", Content: "hi", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(sess.ID, 100, 50); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(sess.ID, 200, 80); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(sess.ID)
	if got.PromptTokens != 300 || got.CompletionTokens != 130 || got.Requests != 2 {
		t.Fatalf("用量累计不符: %+v", got)
	}
	if err := s.AddUsage("bad-id", 1, 1); err == nil {
		t.Fatal("非法 ID 应报错")
	}
}

// 自动命名：SetTitle 写 meta，Get 能读到；已有标题不覆盖；空标题被忽略。
func TestSetTitle(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Create("", "")
	if err := s.SetTitle(sess.ID, "帮我写一个贪吃蛇游戏"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(sess.ID)
	if got.Title != "帮我写一个贪吃蛇游戏" {
		t.Fatalf("标题应写入: %q", got.Title)
	}
	// 已有标题不覆盖
	if err := s.SetTitle(sess.ID, "第二个名字"); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(sess.ID)
	if got2.Title != "帮我写一个贪吃蛇游戏" {
		t.Fatalf("已有标题不应被覆盖: %q", got2.Title)
	}
	// 超长截断 32 rune
	sess2, _ := s.Create("", "")
	long := strings.Repeat("长", 50)
	_ = s.SetTitle(sess2.ID, long)
	got3, _ := s.Get(sess2.ID)
	if len([]rune(got3.Title)) != 32 {
		t.Fatalf("标题应截断到 32 字符，got %d", len([]rune(got3.Title)))
	}
}
