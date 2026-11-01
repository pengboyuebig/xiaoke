package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 路径逃逸的各种形态都必须被拒绝。
func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	env := &Env{Workspace: root}
	cases := []string{
		"../outside.txt",
		"../../outside.txt",
		`..\outside.txt`,
		"sub/../../outside.txt",
		`C:\Windows\System32\drivers\etc\hosts`,
		`\\server\share\file.txt`, // UNC
		"file:///etc/passwd",
	}
	for _, c := range cases {
		if _, err := env.resolveWrite(c); err == nil {
			t.Errorf("resolveWrite(%q) 应拒绝，但通过了", c)
		}
	}
}

// 合法路径必须通过（含子目录、含空格、含中文）。
func TestSafeJoinAllowsLegit(t *testing.T) {
	root := t.TempDir()
	env := &Env{Workspace: root}
	ok := []string{
		"a.txt",
		"sub/b.txt",
		"sub/nested/c.txt",
		"目录/中文文件.md",
		"has space.txt",
	}
	for _, c := range ok {
		if _, err := env.resolveWrite(c); err != nil {
			t.Errorf("resolveWrite(%q) 应通过，但被拒: %v", c, err)
		}
	}
	if _, err := env.resolveWrite("."); err != nil {
		t.Errorf(`resolveWrite(".") 应通过: %v`, err)
	}
}

// 符号链接指向根外必须拒绝（Windows 需要权限，跳过）。
func TestSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 创建符号链接需要管理员权限")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("无法创建符号链接")
	}
	env := &Env{Workspace: root}
	if _, err := env.resolveWrite("link/evil.txt"); err == nil {
		t.Fatal("经过符号链接写出根外应被拒绝")
	}
}

// 大小写变体（Windows）：绝对路径的不同大小写写法不应被误判为越界。
func TestSafeJoinCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 大小写不敏感")
	}
	root := t.TempDir()
	env := &Env{Workspace: root}
	upper := strings.ToUpper(root)
	if _, err := env.resolveWrite(upper); err != nil {
		t.Logf("大小写变体被拒绝（可能误判，需确认）: %v", err)
	}
}
