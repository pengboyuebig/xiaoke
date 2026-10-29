package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(t *testing.T) (*Env, string) {
	t.Helper()
	ws := t.TempDir()
	return &Env{
		Workspace: ws,
		ReadDirs:  []string{filepath.Join(ws, "..", "readonly-xiaoke-test")},
		Allowlist: []string{"git", "python"},
	}, ws
}

func TestSafeJoinRejectsEscape(t *testing.T) {
	env, ws := testEnv(t)
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"正常相对路径", "src/main.py", true},
		{"点路径", ".", true},
		{"子目录穿越再回来", "a/../b.txt", true},
		{"上层逃逸", "../evil.txt", false},
		{"多层逃逸", "a/b/../../../evil.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := env.resolveWrite(c.in)
			if c.ok && err != nil {
				t.Fatalf("应通过却报错: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("应拒绝却通过了: %s (ws=%s)", c.in, ws)
			}
		})
	}
}

func TestResolveReadDeniedWithoutWorkspace(t *testing.T) {
	env := &Env{Workspace: "", ReadDirs: nil, Allowlist: nil}
	if _, err := env.resolveRead("C:/Windows/win.ini"); err == nil {
		t.Fatal("无授权目录时读任意绝对路径应被拒绝")
	}
}

func TestWriteAndReadFile(t *testing.T) {
	env, ws := testEnv(t)
	args, _ := json.Marshal(map[string]string{
		"path":    "sub/hello.txt",
		"content": "你好，小柯\n第二行",
	})
	out, err := execWriteFile(context.Background(), env, args)
	if err != nil {
		t.Fatalf("write_file 失败: %v", err)
	}
	if !strings.Contains(out, "hello.txt") {
		t.Fatalf("写入回执异常: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(ws, "sub", "hello.txt"))
	if err != nil || string(data) != "你好，小柯\n第二行" {
		t.Fatalf("落盘内容不符: %v %q", err, string(data))
	}
	// 读回（带行号头）
	rargs, _ := json.Marshal(map[string]any{"path": "sub/hello.txt"})
	got, err := execReadFile(context.Background(), env, rargs)
	if err != nil || !strings.Contains(got, "第二行") {
		t.Fatalf("read_file 异常: %v %q", err, got)
	}
}

func TestWriteOutsideWorkspaceDenied(t *testing.T) {
	env, _ := testEnv(t)
	args, _ := json.Marshal(map[string]string{"path": "../../evil.txt", "content": "x"})
	if _, err := execWriteFile(context.Background(), env, args); err == nil {
		t.Fatal("越界写入应被拒绝")
	}
}

func TestRunCommandAllowlist(t *testing.T) {
	env, ws := testEnv(t)
	// 白名单外
	bad, _ := json.Marshal(map[string]any{"command": "powershell", "args": []string{"-c", "echo hi"}})
	if _, err := execRunCommand(context.Background(), env, bad); err == nil {
		t.Fatal("白名单外命令应被拒绝")
	}
	// 白名单内（git --version）
	okArgs, _ := json.Marshal(map[string]any{"command": "git", "args": []string{"--version"}})
	out, err := execRunCommand(context.Background(), env, okArgs)
	if err != nil {
		t.Skipf("本机无 git，跳过: %v", err)
	}
	if !strings.Contains(out, "git version") && !strings.Contains(out, "git") {
		t.Fatalf("git 输出异常: %q (ws=%s)", out, ws)
	}
}

func TestSearchFiles(t *testing.T) {
	env, ws := testEnv(t)
	_ = os.WriteFile(filepath.Join(ws, "a.txt"), []byte("foo bar\nBAZ qux\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, "node_modules", "x"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "node_modules", "x", "b.txt"), []byte("foo in deps\n"), 0o644)
	args, _ := json.Marshal(map[string]string{"query": "foo"})
	out, err := execSearchFiles(context.Background(), env, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.txt") || strings.Contains(out, "node_modules") {
		t.Fatalf("搜索应命中 a.txt 且忽略 node_modules: %q", out)
	}
	// 大小写不敏感
	args2, _ := json.Marshal(map[string]string{"query": "baz"})
	out2, _ := execSearchFiles(context.Background(), env, args2)
	if !strings.Contains(out2, "BAZ") {
		t.Fatalf("应大小写不敏感命中: %q", out2)
	}
}

// ToolDef 必须只序列化 OpenAI 协议字段（Exec 函数不能进请求体，否则 json.Marshal 会失败）。
func TestToolDefMarshalOnlyWireFields(t *testing.T) {
	defs := Defs(Builtin())
	if len(defs) == 0 {
		t.Fatal("内置工具为空")
	}
	// 模拟 upstream.go 的请求体构造：body["tools"] = toolDefs
	body := map[string]any{"model": "m", "tools": defs, "stream": true}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化 tools 失败: %v", err)
	}
	var parsed struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
			Exec    *int    `json:"exec"`
			FromMCP *string `json:"fromMCP"`
			MCPTool *string `json:"mcpTool"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, d := range parsed.Tools {
		if d.Type != "function" || d.Function.Name == "" {
			t.Fatalf("协议字段缺失: %+v", d)
		}
		if d.Exec != nil || d.FromMCP != nil || d.MCPTool != nil {
			t.Fatalf("内部字段泄漏进请求体: %+v", d)
		}
	}
}

// run_command 的 cwd 缺省时应跑在项目根（与工具描述一致），不能因空 cwd 报错。
func TestRunCommandDefaultCwd(t *testing.T) {
	env, ws := testEnv(t)
	if strings.TrimSpace(env.Workspace) == "" {
		t.Fatal("测试环境应有 workspace")
	}
	// 用 git --version（无副作用）验证：cwd 留空应成功
	args, _ := json.Marshal(map[string]any{"command": "git", "args": []string{"--version"}})
	out, err := execRunCommand(context.Background(), env, args)
	if err != nil {
		if strings.Contains(err.Error(), "路径不能为空") {
			t.Fatalf("cwd 缺省时不应报「路径不能为空」: %v", err)
		}
		t.Skipf("本机无 git，跳过: %v", err)
	}
	if !strings.Contains(out, "git") {
		t.Fatalf("输出异常: %q (ws=%s)", out, ws)
	}
	// 显式 cwd 仍须在工作区内：越界应拒绝
	bad, _ := json.Marshal(map[string]any{"command": "git", "args": []string{"--version"}, "cwd": "../.."})
	if _, err := execRunCommand(context.Background(), env, bad); err == nil {
		t.Fatal("越界 cwd 应被拒绝")
	}
}
