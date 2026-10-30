package projects

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func newMgr(t *testing.T) (*Manager, string) {
	t.Helper()
	base := t.TempDir()
	m, err := New(filepath.Join(base, "index.json"), func() string {
		return filepath.Join(base, "projects")
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, base
}

func TestCreateListDelete(t *testing.T) {
	m, _ := newMgr(t)
	p, err := m.Create("demo", "code", "", genTestID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path == "" {
		t.Fatal("Path 不能为空")
	}
	if _, err := os.Stat(filepath.Join(p.Path, "README.md")); err != nil {
		t.Fatalf("code 模板应有 README.md: %v", err)
	}
	if len(m.List()) != 1 {
		t.Fatal("List 应有 1 项")
	}
	if _, err := m.Get(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("删除后应为空")
	}
	if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
		t.Fatal("删除项目应连带删除目录")
	}
}

func TestRejectInvalidName(t *testing.T) {
	m, _ := newMgr(t)
	for _, bad := range []string{"", "..", "a/b", "name with space", strings.Repeat("长", 33)} {
		if _, err := m.Create(bad, "", "", genTestID); err == nil {
			t.Fatalf("非法项目名 %q 应被拒绝", bad)
		}
	}
}

func TestRejectDuplicateName(t *testing.T) {
	m, _ := newMgr(t)
	if _, err := m.Create("dup", "", "", genTestID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create("dup", "", "", genTestID); err == nil {
		t.Fatal("同名项目应被拒绝")
	}
}

// 并发同名创建只能成功一次（无 TOCTOU 竞态）。
func TestConcurrentCreateSameName(t *testing.T) {
	m, _ := newMgr(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Create("race", "", "", genTestID)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("并发同名创建应恰好成功 1 次，got %d", ok)
	}
}

// customRoot 指向不存在的目录应被拒绝。
func TestCustomRootMustExist(t *testing.T) {
	m, base := newMgr(t)
	if _, err := m.Create("x", "", filepath.Join(base, "nope"), genTestID); err == nil {
		t.Fatal("自定义目录不存在时应拒绝")
	}
}

// genTestID 是与 api.genID 同格式的 16 位 hex（用原子递增序号避免真实随机）。
func genTestID() string {
	n := seq.Add(1)
	return fmt.Sprintf("%016x", n)
}

var seq atomic.Int64

// 索引残留但目录已删除时，Tree 应明确报错而不是返回空树。
func TestTreeMissingDirErrors(t *testing.T) {
	m, _ := newMgr(t)
	p, err := m.Create("tree-missing", "", "", genTestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tree(p.ID); err == nil {
		t.Fatal("目录不存在时 Tree 应返回 error")
	}
}
