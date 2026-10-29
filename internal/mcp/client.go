// Package mcp 实现最小化的 MCP (Model Context Protocol) stdio 客户端：
// spawn 子进程，走「换行分隔 JSON-RPC 2.0」完成 initialize → tools/list → tools/call。
// 零第三方依赖；协议参考 MCP 2024-11-05 规范与 opensquilla 的 [[mcp.servers]] 配置形态。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"xiaoke/internal/settings"
)

const (
	protocolVersion    = "2024-11-05"
	handshakeTimeout   = 90 * time.Second // npx 冷启动（首次下载）可能要 30-60 秒
	defaultToolTimeout = 60 * time.Second
	lineBuf            = 1 << 20 // 1MB 单行上限
)

// ToolInfo 是 MCP server 声明的一个工具。
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client 是一个 MCP server 的子进程连接（异步就绪）。
type Client struct {
	name    string
	cfg     settings.MCPServer
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stderr  *tailBuffer // 抓 server 报错，握手失败时可诊断
	mu      sync.Mutex  // 序列化请求/响应（一次一个在途请求）
	nextID  int64
	Tools   []ToolInfo
	exited  chan struct{}
	errText string // 非空表示启动/握手失败（伪 Client 仅用于状态展示）

	readyOnce sync.Once
	readyCh   chan struct{}
	readyErr  error
}

// tailBuffer 保留最近 max 字节写入（用于抓 stderr 尾部）。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Start 启动子进程并立即返回；握手在后台进行（Ready 等待完成）。
// 这样「保存设置」不会被 npx 首次下载等慢启动阻塞。
func Start(ctx context.Context, cfg settings.MCPServer) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("command 不能为空")
	}
	c := &Client{
		name: cfg.Name, cfg: cfg, nextID: 1,
		exited: make(chan struct{}), readyCh: make(chan struct{}),
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), envPairs(cfg.Env)...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c.stderr = &tailBuffer{max: 4096}
	cmd.Stderr = c.stderr // 抓 server 报错供诊断
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewScanner(stdout)
	c.stdout.Buffer(make([]byte, lineBuf), lineBuf)
	if err := cmd.Start(); err != nil {
		hint := BinaryHint(err)
		if hint != "" {
			err = fmt.Errorf("%w（%s）", err, hint)
		}
		return nil, fmt.Errorf("启动 MCP server %q 失败: %w", cfg.Command, err)
	}
	go func() { _ = cmd.Wait(); close(c.exited) }()
	go c.backgroundHandshake()
	return c, nil
}

// backgroundHandshake 在后台完成 initialize → initialized → tools/list。
func (c *Client) backgroundHandshake() {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	err := c.handshake(ctx)
	c.readyOnce.Do(func() {
		c.mu.Lock()
		c.readyErr = err
		c.mu.Unlock()
		close(c.readyCh)
	})
}

// Ready 等握手完成，返回握手错误（nil = 已就绪）。
func (c *Client) Ready(ctx context.Context) error {
	select {
	case <-c.readyCh:
		c.mu.Lock()
		err := c.readyErr
		c.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("等待 MCP server %q 启动: %w", c.name, ctx.Err())
	case <-c.exited:
		// 进程退出：再等一次 ready 收敛（握手可能刚失败）
		select {
		case <-c.readyCh:
			c.mu.Lock()
			err := c.readyErr
			c.mu.Unlock()
			return err
		default:
			return fmt.Errorf("MCP server %q 进程已退出%s", c.name, c.stderrSnippet())
		}
	}
}

// stderrSnippet 返回 stderr 末尾用于错误诊断。
func (c *Client) stderrSnippet() string {
	if c.stderr == nil {
		return ""
	}
	s := strings.TrimSpace(c.stderr.String())
	if s == "" {
		return ""
	}
	if rs := []rune(s); len(rs) > 300 {
		s = string(rs[len(rs)-300:])
	}
	return "；server 输出: " + s
}

func envPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, k+"="+v)
	}
	return pairs
}

// handshake：initialize → notifications/initialized → tools/list。
func (c *Client) handshake(ctx context.Context) error {
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "xiaoke", "version": "0.2.0"},
	}, &result); err != nil {
		return fmt.Errorf("initialize 失败: %w%s", err, c.stderrSnippet())
	}
	// 收到响应后再发 initialized 通知（无 id，不等待回应）
	if err := c.notify("notifications/initialized", nil); err != nil {
		return err
	}
	var list struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &list); err != nil {
		return fmt.Errorf("tools/list 失败: %w", err)
	}
	c.mu.Lock()
	c.Tools = list.Tools
	c.mu.Unlock()
	return nil
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func (c *Client) request(ctx context.Context, method string, params map[string]any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.stdin, "%s\n", line); err != nil {
		return fmt.Errorf("写入 stdin: %w%s", err, c.stderrSnippet())
	}
	// 逐行读，跳过通知/请求（无 id 的行），直到拿到匹配 id 的响应
	deadline := time.Now().Add(defaultToolTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = time.Now().Add(time.Until(d))
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s 超时", method)
		}
		if !c.scanOrWait(ctx, deadline) {
			return fmt.Errorf("MCP server %q 已退出或无响应%s", c.name, c.stderrSnippet())
		}
		lineBytes := c.stdout.Bytes()
		if len(lineBytes) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(lineBytes, &resp); err != nil {
			continue // 非 JSON 行（server 杂音）忽略
		}
		if resp.ID != req.ID {
			continue // 通知或别人的响应，忽略
		}
		if resp.Error != nil {
			return fmt.Errorf("MCP 错误 %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

// scanOrWait 等待下一行；返回 false 表示流结束（进程退出）或超时。
func (c *Client) scanOrWait(ctx context.Context, deadline time.Time) bool {
	type res struct{ ok bool }
	ch := make(chan res, 1)
	go func() {
		ok := c.stdout.Scan()
		ch <- res{ok}
	}()
	select {
	case r := <-ch:
		return r.ok
	case <-ctx.Done():
		return false
	case <-time.After(time.Until(deadline)):
		return false
	case <-c.exited:
		return false
	}
}

func (c *Client) notify(method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	nt := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		nt["params"] = params
	}
	line, err := json.Marshal(nt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "%s\n", line)
	return err
}

// CallTool 调用 server 上的一个工具，返回拼接后的文本结果。
func (c *Client) CallTool(ctx context.Context, tool string, args json.RawMessage) (string, error) {
	if args == nil {
		args = json.RawMessage("{}")
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	tctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		// 父 ctx 没 deadline：用默认工具超时
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()
	}
	if err := c.request(tctx, "tools/call", map[string]any{
		"name":      tool,
		"arguments": args,
	}, &result); err != nil {
		return "", err
	}
	texts := make([]string, 0, len(result.Content))
	for _, part := range result.Content {
		if part.Type == "text" || part.Type == "" {
			texts = append(texts, part.Text)
		}
	}
	out := strings.Join(texts, "\n")
	if result.IsError {
		return out, fmt.Errorf("工具执行失败: %s", out)
	}
	return out, nil
}

// Alive 报告子进程是否仍在运行。
func (c *Client) Alive() bool {
	select {
	case <-c.exited:
		return false
	default:
		return true
	}
}

// Stop 结束子进程（进程已退出时立刻返回）。
func (c *Client) Stop() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Kill()
	select {
	case <-c.exited:
	case <-time.After(3 * time.Second):
	}
}

var toolNameSan = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// QualifyName 生成注入给模型的全局工具名：mcp_<server>_<tool>，符合 OpenAI 的
// ^[a-zA-Z0-9_-]{1,64}$ 约束（中文 server 名会被替换成下划线）。
func QualifyName(server, tool string) string {
	s := "mcp_" + toolNameSan.ReplaceAllString(server, "_") + "_" + toolNameSan.ReplaceAllString(tool, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// BinaryHint 返回 npx/node 依赖的友好提示（启动失败时给用户看）。
func BinaryHint(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(filepath.Base(err.Error()), "%") {
		return "未找到命令，请确认已安装 Node.js / 对应运行时并加入 PATH"
	}
	return ""
}
