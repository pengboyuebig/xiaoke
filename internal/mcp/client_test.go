package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"xiaoke/internal/settings"
)

// 用「测试二进制自举子进程」的方式起一个假 MCP server（Windows 友好，不依赖外部脚本）。
const fakeServerEnv = "XIAOKE_MCP_FAKE_SERVER"

func TestFakeMCPServerProcess(t *testing.T) {
	if os.Getenv(fakeServerEnv) != "1" {
		return // 仅作为子进程运行
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		var resp []byte
		switch req.Method {
		case "initialize":
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}},
			})
		case "tools/list":
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": []map[string]any{{
					"name":        "echo",
					"description": "回声测试",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"text": map[string]string{"type": "string"},
					}},
				}}},
			})
		case "tools/call":
			var p struct {
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]string{{"type": "text", "text": "ECHO:" + p.Arguments.Text}},
				},
			})
		default:
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
		fmt.Fprintln(writer, string(resp))
		writer.Flush()
	}
}

// spawnFake 起一个指向自身的假 server，并等握手完成。
func spawnFake(t *testing.T) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.MCPServer{
		Name: "fake", Command: exe,
		Args:    []string{"-test.run=TestFakeMCPServerProcess", "-test.v=false"},
		Env:     map[string]string{fakeServerEnv: "1"},
		Enabled: true,
	}
	cli, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("启动假 server 失败: %v", err)
	}
	t.Cleanup(cli.Stop)
	// 握手是异步的，等就绪后才可断言 tools
	wctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cli.Ready(wctx); err != nil {
		t.Fatalf("假 server 握手失败: %v", err)
	}
	return cli
}

// validToolName 校验 OpenAI 工具名约束 ^[a-zA-Z0-9_-]{1,64}$。
func validToolName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func TestMCPHandshakeAndListTools(t *testing.T) {
	cli := spawnFake(t)
	if len(cli.Tools) != 1 || cli.Tools[0].Name != "echo" {
		t.Fatalf("tools/list 结果异常: %+v", cli.Tools)
	}
	if !strings.Contains(QualifyName("fake", "echo"), "mcp_fake_echo") {
		t.Fatalf("工具名限定异常: %s", QualifyName("fake", "echo"))
	}
	if got := QualifyName("中文服务", "echo"); !validToolName(got) {
		t.Fatalf("中文名净化异常: %q", got)
	}
}

func TestMCPCallTool(t *testing.T) {
	cli := spawnFake(t)
	out, err := cli.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"你好"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "ECHO:你好" {
		t.Fatalf("echo 结果异常: %q", out)
	}
}

// 并发调用必须按请求 ID 正确分发（回归：旧实现超时后会并发读同一个 Scanner）。
func TestMCPConcurrentCalls(t *testing.T) {
	cli := spawnFake(t)
	const n = 8
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := json.RawMessage(fmt.Sprintf(`{"text":"%d"}`, i))
			out, err := cli.CallTool(context.Background(), "echo", args)
			if err != nil {
				errCh <- err
				return
			}
			if want := fmt.Sprintf("ECHO:%d", i); out != want {
				errCh <- fmt.Errorf("第 %d 个调用结果异常: %q != %q", i, out, want)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// readOnly server 的写类工具不能出现在模型可用工具里。
func TestManagerToolsReadOnlyFilter(t *testing.T) {
	m := NewManager(context.Background())
	m.clients["fs"] = &Client{
		name: "fs",
		cfg:  settings.MCPServer{Name: "fs", ReadOnly: true},
		Tools: []ToolInfo{
			{Name: "read_file"},
			{Name: "write_file"},
			{Name: "edit_file"},
			{Name: "create_directory"},
			{Name: "list_directory"},
		},
		exited:   closedChan(),
		readDone: closedChan(),
		readyCh:  closedChan(),
	}
	defs := m.Tools()
	got := map[string]bool{}
	for _, d := range defs {
		got[d.MCPTool] = true
	}
	if !got["read_file"] || !got["list_directory"] {
		t.Fatalf("只读工具应保留: %+v", got)
	}
	for _, name := range []string{"write_file", "edit_file", "create_directory"} {
		if got[name] {
			t.Fatalf("只读 server 不应暴露 %q", name)
		}
	}
}

// 超长工具名截断后必须仍唯一且符合 OpenAI 命名约束。
func TestQualifyNameTruncationUnique(t *testing.T) {
	server := strings.Repeat("s", 80)
	a := QualifyName(server, strings.Repeat("a", 40))
	b := QualifyName(server, strings.Repeat("b", 40))
	if a == b {
		t.Fatalf("不同的长工具名被截成同一个名字: %q", a)
	}
	for _, s := range []string{a, b} {
		if len(s) > 64 || !validToolName(s) {
			t.Fatalf("工具名不符合约束: %q (len=%d)", s, len(s))
		}
	}
}
