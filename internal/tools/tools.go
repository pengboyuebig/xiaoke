// Package tools 提供「小柯」的内置工具集：文件读写、目录浏览、搜索、受限命令。
//
// 安全模型（对标 opensquilla 的 workspace 隔离）：
//   - 写操作与命令执行仅限当前项目工作区（Workspace）
//   - 读操作额外允许 ReadDirs 里的只读授权目录
//   - 命令白名单 + argv 直传（不经 shell）+ 超时 + 输出截断
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Env 是一次工具执行的环境（每次请求按当前设置/项目构造）。
type Env struct {
	Workspace string   // 可读写的工作区根（项目目录）；为空表示未选择项目
	ReadDirs  []string // 只读授权目录
	Allowlist []string // 可执行命令白名单（basename 匹配）
	// CommandTimeoutSec run_command 超时（秒；<=0 用默认 30）。构建/安装类命令可能需要调大。
	CommandTimeoutSec int
}

func (e *Env) clone() *Env {
	cp := *e
	cp.ReadDirs = append([]string(nil), e.ReadDirs...)
	cp.Allowlist = append([]string(nil), e.Allowlist...)
	return &cp
}

// Tool 是一个可被模型调用的工具（OpenAI function 形态）。
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
	Execute     func(ctx context.Context, env *Env, args json.RawMessage) (string, error)
}

// Builtin 返回全部内置工具。
func Builtin() []Tool {
	return []Tool{toolListFiles, toolReadFile, toolWriteFile, toolSearchFiles, toolRunCommand}
}

// ToolDef 是发给上游 OpenAI 接口的 tools 数组元素。
type ToolDef struct {
	Type     string                                                                    `json:"type"`
	Function ToolFunction                                                              `json:"function"`
	Exec     func(ctx context.Context, env *Env, args json.RawMessage) (string, error) `json:"-"`
	FromMCP  string                                                                    `json:"-"` // 非空表示来自该 MCP server
	MCPTool  string                                                                    `json:"-"` // MCP server 侧的原始工具名
}

// MarshalJSON 只输出 OpenAI 协议字段，避免把 Exec 函数（不可序列化）带进请求体。
func (d ToolDef) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type     string       `json:"type"`
		Function ToolFunction `json:"function"`
	}
	return json.Marshal(wire{Type: d.Type, Function: d.Function})
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Defs 把工具列表转成上游请求形态（内置工具可直接执行）。
func Defs(all []Tool) []ToolDef {
	defs := make([]ToolDef, 0, len(all))
	for _, t := range all {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
			Exec: t.Execute,
		})
	}
	return defs
}

// IsWriteCapable 判断一个工具名是否具有写入/修改/执行副作用。
// 用于两个地方：
//   - 计划模式：只保留只读工具
//   - MCP server 标记 readOnly：从暴露给模型的工具列表里过滤掉写类工具
//
// 内置工具按精确名判断；MCP 工具名不统一，按常见动词模式匹配。
func IsWriteCapable(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	switch n {
	case "write_file", "run_command":
		return true
	}
	for _, kw := range []string{
		"write", "edit", "create", "mkdir", "move", "rename", "delete", "remove",
		"patch", "append", "insert", "replace", "copy", "truncate", "chmod",
		"chown", "symlink", "upload", "save", "persist", "update", "mkfile",
		"put_", "exec", "run_", "shell", "terminal",
	} {
		if strings.Contains(n, kw) {
			return true
		}
	}
	// rm/mv/cp/touch 这类两字母动词按「整词」匹配，避免误伤 format/confirm 等单词。
	for _, w := range []string{"rm", "mv", "cp", "touch"} {
		if n == w || strings.HasPrefix(n, w+"_") || strings.HasSuffix(n, "_"+w) ||
			strings.Contains(n, "_"+w+"_") || strings.Contains(n, w+"_file") {
			return true
		}
	}
	return false
}

// ---------- 路径安全 ----------

// resolveRead 把用户路径解析为可读的绝对路径：先试工作区，再试只读目录。
func (e *Env) resolveRead(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	if e.Workspace != "" {
		if abs, err := safeJoin(e.Workspace, p); err == nil {
			return abs, nil
		}
	}
	for _, dir := range e.ReadDirs {
		if abs, err := safeJoin(dir, p); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("路径 %q 不在工作区或已授权的只读目录内", p)
}

// resolveWrite 把用户路径解析为工作区内的可写绝对路径（未选项目时报错）。
func (e *Env) resolveWrite(p string) (string, error) {
	if strings.TrimSpace(e.Workspace) == "" {
		return "", fmt.Errorf("尚未选择项目：请先创建或选择一个项目，才能写入文件")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	return safeJoin(e.Workspace, p)
}

// safeJoin 把用户相对路径安全地拼到 root 下，拒绝一切逃逸（..、绝对路径注入、盘符），
// 并解析符号链接/junction，防止工作区内的链接指向外部。
func safeJoin(root, userPath string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// 绝对路径/盘符开头：只允许它本来就等于 root 或在 root 下
	clean := filepath.Clean(userPath)
	if filepath.IsAbs(clean) {
		if _, err := relChecked(absRoot, clean); err != nil {
			return "", err
		}
		if err := ensureNoSymlinkEscape(absRoot, clean); err != nil {
			return "", err
		}
		return clean, nil
	}
	joined := filepath.Join(absRoot, clean)
	if _, err := relChecked(absRoot, joined); err != nil {
		return "", err
	}
	if err := ensureNoSymlinkEscape(absRoot, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// ensureNoSymlinkEscape 解析 target 最深的已存在祖先的真实路径，
// 确认它仍在 root 的真实路径内。root 不存在时跳过（后续 os 操作会报错）。
func ensureNoSymlinkEscape(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	probe := target
	suffix := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return nil // 权限等错误不在这一层判断
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		suffix = append([]string{filepath.Base(probe)}, suffix...)
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return fmt.Errorf("解析路径失败: %w", err)
	}
	realTarget := filepath.Join(append([]string{realProbe}, suffix...)...)
	if _, err := relChecked(realRoot, realTarget); err != nil {
		return fmt.Errorf("路径经过符号链接越出授权根目录: %w", err)
	}
	return nil
}

// relChecked 校验 target 必须位于 root 内（含 root 本身），返回相对路径。
func relChecked(root, target string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("路径越界: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %q 越出授权根目录", target)
	}
	return rel, nil
}
