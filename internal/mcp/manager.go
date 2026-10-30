package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"xiaoke/internal/settings"
	"xiaoke/internal/tools"
)

// Status 是设置页展示的每个 server 的运行状态。
type Status struct {
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	ReadOnly bool     `json:"readOnly,omitempty"`
	Running  bool     `json:"running"`
	Starting bool     `json:"starting"`
	Error    string   `json:"error,omitempty"`
	Tools    []string `json:"tools"`
	ToolHint string   `json:"toolHint,omitempty"`
}

// Manager 管理全部启用的 MCP server 常驻进程：
// Apply 时对比新旧配置，停掉移除/禁用/变更的，启动新增的；热更新不重启未变化的。
type Manager struct {
	mu      sync.Mutex
	ctx     context.Context
	clients map[string]*Client
	servers []settings.MCPServer
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{ctx: ctx, clients: map[string]*Client{}}
}

// Apply 按新配置收敛进程状态（设置保存后调用）。启动是异步的，不会阻塞保存。
func (m *Manager) Apply(servers []settings.MCPServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers = append([]settings.MCPServer(nil), servers...)
	want := map[string]settings.MCPServer{}
	for _, s := range servers {
		if s.Enabled && strings.TrimSpace(s.Name) != "" {
			want[s.Name] = s
		}
	}
	// 停止：不存在/被禁用/配置变更
	for name, cli := range m.clients {
		w, keep := want[name]
		if !keep || !sameServer(cli.cfg, w) {
			cli.Stop()
			delete(m.clients, name)
		}
	}
	// 启动新增 / 重试失败或已退出的
	for name, s := range want {
		if cli, ok := m.clients[name]; ok {
			cli.mu.Lock()
			readyErr, readyDone := cli.readyErr, cli.readyClosed()
			startErr := cli.errText
			cli.mu.Unlock()
			// 启动失败、进程退出、握手失败都允许下一次 Apply 重试；
			// 正在握手中的保持不动（npx 冷启动可能几十秒）。
			recoverable := !cli.Alive() || startErr != "" || (readyDone && readyErr != nil)
			if !recoverable {
				continue
			}
			cli.Stop()
			delete(m.clients, name)
		}
		cli, err := Start(m.ctx, s)
		if err != nil {
			// 启动失败记录在伪 Client 里，状态页可见，不阻塞其它 server
			m.clients[name] = &Client{
				name: name, cfg: s,
				exited:   closedChan(),
				readDone: closedChan(),
				readyCh:  closedChan(),
				readyErr: err,
				errText:  err.Error(),
			}
			continue
		}
		m.clients[name] = cli
	}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func sameServer(a, b settings.MCPServer) bool {
	if a.Command != b.Command || len(a.Args) != len(b.Args) || len(a.Env) != len(b.Env) ||
		a.ReadOnly != b.ReadOnly {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	for k, v := range a.Env {
		if b.Env[k] != v {
			return false
		}
	}
	return true
}

// Status 返回全部 server 的状态快照。
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.servers))
	for _, s := range m.servers {
		st := Status{Name: s.Name, Enabled: s.Enabled, ReadOnly: s.ReadOnly, Tools: []string{}}
		cli, ok := m.clients[s.Name]
		if !ok || cli == nil {
			if s.Enabled {
				st.Error = "未启动"
			}
			out = append(out, st)
			continue
		}
		cli.mu.Lock()
		st.Error = cli.errText
		if cli.errText == "" {
			st.Running = cli.Alive()
			for _, t := range cli.Tools {
				st.Tools = append(st.Tools, t.Name)
			}
			// readyCh 未闭合 = 握手还在进行（npx 冷启动）
			if s.Enabled && !st.Running {
				st.Error = "进程已退出"
			} else if s.Enabled && !cli.readyClosed() {
				st.Starting = true
			} else if s.Enabled && cli.readyErr != nil {
				st.Error = cli.readyErr.Error()
			}
		} else if hint := BinaryHint(fmt.Errorf("%s", st.Error)); hint != "" {
			st.ToolHint = hint
		}
		cli.mu.Unlock()
		out = append(out, st)
	}
	return out
}

// readyClosed 报告握手是否已收敛（成功或失败）。
func (c *Client) readyClosed() bool {
	select {
	case <-c.readyCh:
		return true
	default:
		return false
	}
}

// Tools 返回全部已就绪 server 的工具，转为可注入模型的 ToolDef。
func (m *Manager) Tools() []tools.ToolDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	var defs []tools.ToolDef
	for _, cli := range m.clients {
		cli.mu.Lock()
		for _, t := range cli.Tools {
			// readOnly server：写/删/移动类工具不暴露给模型（内置写工具也走同一判定）
			if cli.cfg.ReadOnly && tools.IsWriteCapable(t.Name) {
				continue
			}
			name := QualifyName(cli.name, t.Name)
			desc := fmt.Sprintf("[MCP:%s] %s", cli.name, strings.TrimSpace(t.Description))
			if strings.TrimSpace(t.Description) == "" {
				desc = fmt.Sprintf("[MCP:%s] %s", cli.name, t.Name)
			}
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			serverName, origName := cli.name, t.Name
			defs = append(defs, tools.ToolDef{
				Type:     "function",
				Function: tools.ToolFunction{Name: name, Description: desc, Parameters: schema},
				FromMCP:  serverName,
				MCPTool:  origName,
				Exec:     nil, // 执行走 Manager.CallTool
			})
		}
		cli.mu.Unlock()
	}
	return defs
}

// Call 调用指定 server 的工具；若握手尚未完成，最多等 30 秒。
func (m *Manager) Call(ctx context.Context, server, tool string, args json.RawMessage) (string, error) {
	m.mu.Lock()
	cli := m.clients[server]
	m.mu.Unlock()
	if cli == nil {
		return "", fmt.Errorf("MCP server %q 未运行", server)
	}
	if cli.errText != "" {
		return "", fmt.Errorf("MCP server %q 启动失败: %s", server, cli.errText)
	}
	// 就绪检查（npx 冷启动可能还在握手）
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.Ready(wctx); err != nil {
		return "", err
	}
	return cli.CallTool(ctx, tool, args)
}

// Close 停止所有 MCP 子进程（应用退出时调用）。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, cli := range m.clients {
		cli.Stop()
		delete(m.clients, name)
	}
}
