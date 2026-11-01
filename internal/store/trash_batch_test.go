package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 批量彻底删除：全部成功时返回条数正确，回收站清空。
func TestPurgeTrashBatchAll(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, name := range []string{"甲", "乙", "丙"} {
		sess, err := s.Create(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(sess.ID); err != nil {
			t.Fatal(err)
		}
		items, err := s.ListTrash()
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ID == sess.ID {
				dirs = append(dirs, it.Dir)
			}
		}
	}
	if len(dirs) != 3 {
		t.Fatalf("应有 3 条待删记录，实际 %d", len(dirs))
	}

	ok, errs := s.PurgeTrashBatch(dirs)
	if ok != 3 || len(errs) != 0 {
		t.Fatalf("应全部删除成功，got ok=%d errs=%v", ok, errs)
	}
	if left, _ := s.ListTrash(); len(left) != 0 {
		t.Errorf("回收站应已清空，剩 %d 条", len(left))
	}
}

// 批量删除：个别目录不存在时应跳过并计入失败，其余照常删除。
func TestPurgeTrashBatchPartial(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("真会话", "")
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
	real := items[0].Dir
	fake := sess.ID + "-20200101-000000" // 不存在的目录

	ok, errs := s.PurgeTrashBatch([]string{real, fake})
	if ok != 1 {
		t.Errorf("应成功删除 1 条，实际 %d", ok)
	}
	if len(errs) != 1 {
		t.Errorf("应有 1 条失败记录，实际 %d: %v", len(errs), errs)
	}
	if left, _ := s.ListTrash(); len(left) != 0 {
		t.Errorf("真实记录应已删除，剩 %d 条", len(left))
	}
}

// 批量删除：非法目录名（不符合命名格式）必须被拒绝。
func TestPurgeTrashBatchRejectsInvalidName(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 用路径穿越样式的名字，PurgeTrash 应因命名不匹配而拒绝
	ok, errs := s.PurgeTrashBatch([]string{"../../../etc"})
	if ok != 0 {
		t.Errorf("非法目录名不应删除成功，实际 ok=%d", ok)
	}
	if len(errs) != 1 || !errors.Is(errs[0], ErrTrashNotFound) {
		// errs[0] 是包装后的错误，用 Unwrap 检查根因
		if len(errs) != 1 {
			t.Fatalf("应有 1 条错误，实际 %v", errs)
		}
		if !errors.Is(errs[0], ErrTrashNotFound) {
			t.Errorf("错误根因应为 ErrTrashNotFound，实际 %v", errs[0])
		}
	}
}

// 空列表：不报错，返回 0。
func TestPurgeTrashBatchEmpty(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ok, errs := s.PurgeTrashBatch(nil)
	if ok != 0 || len(errs) != 0 {
		t.Errorf("空列表应返回 0/0，got ok=%d errs=%v", ok, errs)
	}
}

// 确认批量删除真的把磁盘目录删掉了（不只是索引）。
func TestPurgeTrashBatchRemovesDisk(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create("x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	items, _ := s.ListTrash()
	if len(items) != 1 {
		t.Fatalf("应有 1 条记录")
	}
	dir := filepath.Join(s.TrashDir(), items[0].Dir)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("删除前目录应存在: %v", err)
	}
	if _, errs := s.PurgeTrashBatch([]string{items[0].Dir}); len(errs) != 0 {
		t.Fatalf("批量删除失败: %v", errs)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("目录应已从磁盘删除，stat err=%v", err)
	}
}
