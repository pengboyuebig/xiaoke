package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func writeTemp(t *testing.T, env *Env, path, content string) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	if _, err := execWriteFile(context.Background(), env, args); err != nil {
		t.Fatalf("writeTemp 失败: %v", err)
	}
}

func TestEditFileBasic(t *testing.T) {
	env, ws := testEnv(t)
	writeTemp(t, env, "app.py", "def main():\n    print(\"hello\")\n    return 0\n")
	args, _ := json.Marshal(map[string]string{
		"path":       "app.py",
		"old_string": "print(\"hello\")",
		"new_string": "print(\"hello, xiaoke\")",
	})
	out, err := execEditFile(context.Background(), env, args)
	if err != nil {
		t.Fatalf("edit_file 失败: %v", err)
	}
	if !strings.Contains(out, "替换 1 处") {
		t.Fatalf("回执异常: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "app.py"))
	want := "def main():\n    print(\"hello, xiaoke\")\n    return 0\n"
	if string(data) != want {
		t.Fatalf("替换后内容不符: %q", string(data))
	}
}

func TestEditFileNoMatchGivesHint(t *testing.T) {
	env, _ := testEnv(t)
	writeTemp(t, env, "a.txt", "alpha beta\ngamma delta\n")
	// 首行能对上但整段不一致：提示应给出「第 1 行附近」
	args, _ := json.Marshal(map[string]string{
		"path": "a.txt", "old_string": "alpha beta\nWRONG LINE", "new_string": "x",
	})
	_, err := execEditFile(context.Background(), env, args)
	if err == nil || !strings.Contains(err.Error(), "第 1 行") {
		t.Fatalf("零匹配应附首行位置提示: %v", err)
	}
	// 首行也对不上：退化到最长 token 提示
	args2, _ := json.Marshal(map[string]string{
		"path": "a.txt", "old_string": "alpha beta2 gamma", "new_string": "x",
	})
	_, err = execEditFile(context.Background(), env, args2)
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("零匹配应附最近似内容提示: %v", err)
	}
}

func TestEditFileMultipleMatches(t *testing.T) {
	env, ws := testEnv(t)
	writeTemp(t, env, "b.txt", "same\nsame\nother\n")
	args, _ := json.Marshal(map[string]string{
		"path": "b.txt", "old_string": "same", "new_string": "changed",
	})
	if _, err := execEditFile(context.Background(), env, args); err == nil {
		t.Fatal("多处匹配且未 replace_all 应报错")
	}
	all, _ := json.Marshal(map[string]any{
		"path": "b.txt", "old_string": "same", "new_string": "changed", "replace_all": true,
	})
	if _, err := execEditFile(context.Background(), env, all); err != nil {
		t.Fatalf("replace_all 应成功: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "b.txt"))
	if string(data) != "changed\nchanged\nother\n" {
		t.Fatalf("replace_all 结果不符: %q", string(data))
	}
}

func TestEditFilePreservesCRLF(t *testing.T) {
	env, ws := testEnv(t)
	p := filepath.Join(ws, "win.txt")
	if err := os.WriteFile(p, []byte("line1\r\nline2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{
		"path": "win.txt", "old_string": "line2", "new_string": "line2 edited",
	})
	if _, err := execEditFile(context.Background(), env, args); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "line1\r\nline2 edited\r\n" {
		t.Fatalf("CRLF 文件编辑后应保持 CRLF: %q", string(data))
	}
}

func TestEditFileRejects(t *testing.T) {
	env, _ := testEnv(t)
	writeTemp(t, env, "c.txt", "content\n")
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"old 为空", map[string]any{"path": "c.txt", "old_string": "", "new_string": "x"}, "old_string 不能为空"},
		{"old==new", map[string]any{"path": "c.txt", "old_string": "content", "new_string": "content"}, "相同"},
		{"文件不存在", map[string]any{"path": "nope.txt", "old_string": "a", "new_string": "b"}, "文件不存在"},
		{"越界路径", map[string]any{"path": "../evil.txt", "old_string": "a", "new_string": "b"}, "越出授权根目录"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, _ := json.Marshal(c.args)
			_, err := execEditFile(context.Background(), env, args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("应报错含 %q，实际: %v", c.want, err)
			}
		})
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

// run_command 在工作目录不存在时必须返回 error（原先 chdir 失败被当成功返回）。
func TestRunCommandMissingCwdFails(t *testing.T) {
	env := &Env{
		Workspace: filepath.Join(t.TempDir(), "not-exist-yet"),
		Allowlist: []string{"git"},
	}
	args, _ := json.Marshal(map[string]any{"command": "git", "args": []string{"--version"}})
	out, err := execRunCommand(context.Background(), env, args)
	if err == nil {
		t.Fatalf("目录不存在应返回 error，实际 out=%q", out)
	}
	if !strings.Contains(err.Error(), "工作目录不存在") {
		t.Fatalf("错误信息应说明目录不存在: %v", err)
	}
}

func TestIsWriteCapable(t *testing.T) {
	write := []string{
		"write_file", "run_command", "write_text_file", "edit_file",
		"create_directory", "move_file", "delete_file", "append_note",
		"insert_text", "copy_file", "patch_file", "shell_exec", "upload_file",
		"rm_file", "mv_file", "cp_file", "touch_file", "truncate_file",
		"save_file", "persist_state", "update_file", "mkfile", "put_file",
	}
	for _, n := range write {
		if !IsWriteCapable(n) {
			t.Errorf("%q 应判定为写类工具", n)
		}
	}
	read := []string{
		"read_file", "read_text_file", "list_directory", "list_files",
		"search_files", "get_file_info", "directory_tree", "read_multiple_files",
		"list_allowed_directories", "format_text", "confirm_status",
		// 智能体内部状态工具：只写自己的待办/记忆，不碰用户文件
		"todo_write", "memory_append", "memory_read",
	}
	for _, n := range read {
		if IsWriteCapable(n) {
			t.Errorf("%q 不应判定为写类工具", n)
		}
	}
}

// todo_write：状态校验、单 in_progress 约束、回调透传。
func TestTodoWrite(t *testing.T) {
	env := &Env{}
	if _, err := execTodoWrite(context.Background(), env, nil); err == nil {
		t.Fatal("回调未注入时应报错")
	}
	var saved []TodoItem
	env.SetTodos = func(todos []TodoItem) error {
		saved = todos
		return nil
	}
	ok, _ := json.Marshal(map[string]any{"todos": []map[string]string{
		{"content": "调研", "status": "completed"},
		{"content": "实现", "status": "in_progress"},
		{"content": "测试", "status": "pending"},
	}})
	if out, err := execTodoWrite(context.Background(), env, ok); err != nil {
		t.Fatalf("合法待办应通过: %v %q", err, out)
	}
	if len(saved) != 3 || saved[0].Status != "completed" {
		t.Fatalf("待办未透传: %+v", saved)
	}
	// 两个 in_progress 拒绝
	bad, _ := json.Marshal(map[string]any{"todos": []map[string]string{
		{"content": "a", "status": "in_progress"},
		{"content": "b", "status": "in_progress"},
	}})
	if _, err := execTodoWrite(context.Background(), env, bad); err == nil {
		t.Fatal("多个 in_progress 应拒绝")
	}
	// 非法状态拒绝
	bad2, _ := json.Marshal(map[string]any{"todos": []map[string]string{
		{"content": "a", "status": "done"},
	}})
	if _, err := execTodoWrite(context.Background(), env, bad2); err == nil {
		t.Fatal("非法状态应拒绝")
	}
	// 空列表拒绝
	empty, _ := json.Marshal(map[string]any{"todos": []map[string]string{}})
	if _, err := execTodoWrite(context.Background(), env, empty); err == nil {
		t.Fatal("空待办应拒绝")
	}
}

// memory 工具依赖 Env 注入的回调：回调缺失要明确报错，存在则透传 scope。
func TestMemoryToolsViaEnvCallbacks(t *testing.T) {
	env := &Env{}
	args, _ := json.Marshal(map[string]string{"content": "记住我", "scope": "global"})
	if _, err := execMemoryAppend(context.Background(), env, args); err == nil {
		t.Fatal("回调未注入时 memory_append 应报错")
	}
	if _, err := execMemoryRead(context.Background(), env, nil); err == nil {
		t.Fatal("回调未注入时 memory_read 应报错")
	}

	var gotScope, gotContent string
	env.MemoryAppend = func(scope, content string) (string, error) {
		gotScope, gotContent = scope, content
		return "已写入", nil
	}
	env.MemoryRead = func(scope string) (string, error) {
		return "记忆内容(" + scope + ")", nil
	}
	if out, err := execMemoryAppend(context.Background(), env, args); err != nil || out != "已写入" {
		t.Fatalf("透传失败: %v %q", err, out)
	}
	if gotScope != "global" || gotContent != "记住我" {
		t.Fatalf("参数未正确透传: scope=%q content=%q", gotScope, gotContent)
	}
	// scope 缺省 = project
	defArgs, _ := json.Marshal(map[string]string{"content": "默认作用域"})
	if _, err := execMemoryAppend(context.Background(), env, defArgs); err != nil {
		t.Fatal(err)
	}
	if gotScope != "project" {
		t.Fatalf("scope 缺省应为 project: %q", gotScope)
	}
	rargs, _ := json.Marshal(map[string]string{})
	out, err := execMemoryRead(context.Background(), env, rargs)
	if err != nil || !strings.Contains(out, "project") {
		t.Fatalf("memory_read 缺省 scope 异常: %v %q", err, out)
	}
}

// 非 0 退出必须返回 error，避免 UI/模型把失败当成功。
func TestRunCommandNonZeroExitIsError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在 Windows 上用 cmd 验证退出码")
	}
	env, _ := testEnv(t)
	env.Allowlist = []string{"cmd"}
	args, _ := json.Marshal(map[string]any{"command": "cmd", "args": []string{"/c", "exit", "1"}})
	out, err := execRunCommand(context.Background(), env, args)
	if err == nil {
		t.Fatalf("非 0 退出应返回 error，实际 out=%q", out)
	}
	if !strings.Contains(err.Error(), "退出码") {
		t.Fatalf("错误信息应包含退出码: %v", err)
	}
	if !strings.Contains(out, "$ cmd") {
		t.Fatalf("错误结果仍应带上命令输出: %q", out)
	}
}

// 工作区内的符号链接/junction 不能成为逃逸通道；指向工作区内部则允许。
func TestSafeJoinRejectsSymlinkEscape(t *testing.T) {
	env, ws := testEnv(t)
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接（Windows 需要开发者模式/权限）: %v", err)
	}
	if _, err := env.resolveWrite("link/evil.txt"); err == nil {
		t.Fatal("通过符号链接写出工作区应被拒绝")
	}
	env.ReadDirs = nil
	if _, err := env.resolveRead("link"); err == nil {
		t.Fatal("通过符号链接读取工作区外应被拒绝")
	}

	// 指向工作区内部的链接应允许
	real := filepath.Join(ws, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(ws, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, err := env.resolveWrite("alias/ok.txt"); err != nil {
		t.Fatalf("指向工作区内部的符号链接应允许: %v", err)
	}
}
