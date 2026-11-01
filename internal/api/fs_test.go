package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"xiaoke/internal/projects"
)

// newDirsHandler 构造只含 projects 的最小 Handler（listDirs 只用到 projects.Root）。
func newDirsHandler(t *testing.T, root string) *Handler {
	t.Helper()
	pm, err := projects.New(filepath.Join(t.TempDir(), "projects.json"), func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{projects: pm}
}

func getDirs(t *testing.T, h *Handler, path string) (fsDirsResp, int) {
	t.Helper()
	url := "/api/fs/dirs"
	if path != "" {
		url += "?path=" + path
	}
	rec := httptest.NewRecorder()
	h.listDirs(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var out fsDirsResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON: %v", err)
		}
	}
	return out, rec.Code
}

// 空 path 时从默认项目根目录开始。
func TestListDirsDefaultRoot(t *testing.T) {
	root := t.TempDir()
	h := newDirsHandler(t, root)

	// 在根下建几个子目录 + 一个文件（文件不应出现在结果里）
	for _, name := range []string{"beta", "Alpha", "中文目录"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := getDirs(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", code)
	}
	if len(out.Dirs) != 3 {
		t.Fatalf("应只列出 3 个子目录（排除文件），实际 %d: %+v", len(out.Dirs), out.Dirs)
	}
	// 大小写不敏感排序：Alpha 在 beta 前
	if out.Dirs[0].Name != "Alpha" {
		t.Errorf("排序应大小写不敏感，首个应为 Alpha，实际 %q", out.Dirs[0].Name)
	}
	for _, d := range out.Dirs {
		if strings.HasSuffix(d.Path, "a.txt") {
			t.Fatal("文件不应出现在目录列表里")
		}
	}
	if !filepath.IsAbs(out.Path) {
		t.Errorf("返回的 path 应是绝对路径，实际 %q", out.Path)
	}
}

// 指定 path 时列出该目录的子目录，并给出正确的 parent。
func TestListDirsNavigate(t *testing.T) {
	root := t.TempDir()
	h := newDirsHandler(t, root)
	sub := filepath.Join(root, "parent", "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "parent", "sibling"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := getDirs(t, h, filepath.Join(root, "parent"))
	if code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", code)
	}
	if len(out.Dirs) != 2 {
		t.Fatalf("parent 下应有 2 个子目录，实际 %d", len(out.Dirs))
	}
	if out.Parent == "" {
		t.Fatal("非根目录应返回 parent")
	}
	if !strings.EqualFold(out.Parent, root) {
		t.Errorf("parent 应为 %q，实际 %q", root, out.Parent)
	}
}

// 不存在的目录返回 404；传入文件路径返回 400。
func TestListDirsErrors(t *testing.T) {
	root := t.TempDir()
	h := newDirsHandler(t, root)

	_, code := getDirs(t, h, filepath.Join(root, "not-exist"))
	if code != http.StatusNotFound {
		t.Errorf("不存在的目录应 404，实际 %d", code)
	}

	f := filepath.Join(root, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, code = getDirs(t, h, f)
	if code != http.StatusBadRequest {
		t.Errorf("传入文件路径应 400，实际 %d", code)
	}
}

// 空目录返回空列表而不是报错。
func TestListDirsEmpty(t *testing.T) {
	root := t.TempDir()
	h := newDirsHandler(t, root)
	out, code := getDirs(t, h, root)
	if code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", code)
	}
	if len(out.Dirs) != 0 {
		t.Fatalf("空目录应返回空列表，实际 %+v", out.Dirs)
	}
	if out.Truncated {
		t.Error("空目录不应标记截断")
	}
}

// baseName / parentDir 的边界：盘符根与普通目录。
func TestBaseNameAndParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "somewhere")
	if got := baseName(dir); got != "somewhere" {
		t.Errorf("baseName = %q，应为 somewhere", got)
	}
	if got := parentDir(dir); got == "" {
		t.Error("普通目录应有 parent")
	}
	if got := parentDir(t.TempDir()); got == "" {
		t.Error("临时目录应有 parent")
	}
}

// 盘符根：baseName 应显示 "D:\"，parent 应为空（已到顶）。
func TestVolumeRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 有盘符根")
	}
	root := `D:\`
	if got := baseName(root); got != root {
		t.Errorf("盘符根 baseName = %q，应为 %q", got, root)
	}
	if got := parentDir(root); got != "" {
		t.Errorf("盘符根 parent 应为空，实际 %q", got)
	}
}

// ---------- findDir（拖拽文件夹用） ----------

func findDirReq(t *testing.T, h *Handler, name string) (findDirResp, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.findDir(rec, httptest.NewRequest(http.MethodGet, "/api/fs/find-dir?name="+name, nil))
	var out findDirResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON: %v", err)
		}
	}
	return out, rec.Code
}

// 空 name 应 400。
func TestFindDirRequiresName(t *testing.T) {
	h := newDirsHandler(t, t.TempDir())
	if _, code := findDirReq(t, h, ""); code != http.StatusBadRequest {
		t.Errorf("空 name 应 400，实际 %d", code)
	}
}

// 在项目根下能按名字找到同名目录（大小写不敏感）。
func TestFindDirInProjectRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "drag-target-unique")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newDirsHandler(t, root)

	out, code := findDirReq(t, h, "drag-target-unique")
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", code)
	}
	found := false
	for _, d := range out.Dirs {
		if strings.EqualFold(d.Path, target) {
			found = true
		}
	}
	if !found {
		t.Fatalf("应找到 %q，实际结果 %+v", target, out.Dirs)
	}

	// 大小写不敏感
	out2, _ := findDirReq(t, h, "DRAG-TARGET-UNIQUE")
	if len(out2.Dirs) == 0 {
		t.Error("查找应大小写不敏感")
	}
}

// 不存在的名字返回空列表（不是错误）。
func TestFindDirNotFound(t *testing.T) {
	h := newDirsHandler(t, t.TempDir())
	out, code := findDirReq(t, h, "no-such-folder-zzz-999")
	if code != http.StatusOK {
		t.Fatalf("找不到不应报错，实际 %d", code)
	}
	if len(out.Dirs) != 0 {
		t.Errorf("不应返回任何候选，实际 %+v", out.Dirs)
	}
}

// 跳过 node_modules 等依赖目录。
func TestFindDirSkipsDependencyDirs(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "inner-target")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newDirsHandler(t, root)
	out, _ := findDirReq(t, h, "inner-target")
	for _, d := range out.Dirs {
		if strings.Contains(d.Path, "node_modules") {
			t.Errorf("不应进入 node_modules：%q", d.Path)
		}
	}
}

// 默认不扫全盘（避免拖拽卡住）；只有 deep=1 才扫盘。
func TestFindDirDeepOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	h := newDirsHandler(t, root)

	// 造一个名字独特、且不在项目根/主目录里的目录（模拟全盘深处的目录）
	// 这里只验证「默认不扫盘」这一行为：随便一个不存在的名字都应快速返回空。
	start := time.Now()
	out, code := findDirReq(t, h, "zzz-not-anywhere-999")
	elapsed := time.Since(start)
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", code)
	}
	if len(out.Dirs) != 0 {
		t.Errorf("不应有候选，实际 %+v", out.Dirs)
	}
	// 默认路径只搜项目根+主目录，应远快于全盘扫描
	if elapsed > 3*time.Second {
		t.Errorf("默认查找不应超过 3 秒（说明可能在扫全盘），实际 %v", elapsed)
	}
}
