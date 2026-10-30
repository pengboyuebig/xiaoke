package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxReadBytes   = 64 * 1024  // read_file 单次上限
	maxWriteBytes  = 256 * 1024 // write_file 单次上限
	maxListEntries = 500        // 目录浏览条目上限
	maxSearchHits  = 100        // 搜索命中上限
	maxSearchFiles = 2000       // 搜索扫描文件数上限
	maxOutBytes    = 8 * 1024   // 命令输出截断
	cmdTimeout     = 30 * time.Second
)

var toolListFiles = Tool{
	Name:        "list_files",
	Description: "浏览目录：列出工作区（或授权只读目录）内 path 下的文件与子目录。recursive=true 时递归（深度≤4）。返回相对路径列表，d 开头为目录。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "相对路径，默认 \".\"（工作区根）"},
    "recursive": {"type": "boolean", "description": "是否递归列出，默认 false"}
  }
}`),
	Execute: execListFiles,
}

var toolReadFile = Tool{
	Name:        "read_file",
	Description: "读取文本文件内容。offset/limit 按行截取（默认从头读全部，上限 64KB）。写代码前先用它了解现状。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件相对路径"},
    "offset": {"type": "integer", "description": "起始行号（1 开始），默认 1"},
    "limit": {"type": "integer", "description": "最多读取行数，默认全部（≤64KB）"}
  },
  "required": ["path"]
}`),
	Execute: execReadFile,
}

var toolWriteFile = Tool{
	Name:        "write_file",
	Description: "把文本写入工作区内的文件（自动创建父目录，UTF-8，覆盖式写入，上限 256KB）。只能写当前项目工作区内的路径。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件相对路径（相对项目根）"},
    "content": {"type": "string", "description": "完整文件内容"}
  },
  "required": ["path", "content"]
}`),
	Execute: execWriteFile,
}

var toolSearchFiles = Tool{
	Description: "在工作区内做全文搜索（大小写不敏感的子串匹配），返回「文件:行号: 内容」列表（≤100 条）。忽略 node_modules/.git 等目录和大于 1MB 的文件。",
	Name:        "search_files",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索关键词"},
    "path": {"type": "string", "description": "搜索起点，默认工作区根"}
  },
  "required": ["query"]
}`),
	Execute: execSearchFiles,
}

var toolRunCommand = Tool{
	Description: "在工作区内执行白名单命令（git/npm/npx/node/python/pip）。argv 直传不经 shell，30 秒超时，输出截断 8KB。例：command=\"git\", args=[\"status\"]。",
	Name:        "run_command",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "命令名，如 git、npm、python"},
    "args": {"type": "array", "items": {"type": "string"}, "description": "参数列表"},
    "cwd": {"type": "string", "description": "工作目录（相对项目根，默认项目根）"}
  },
  "required": ["command"]
}`),
	Execute: execRunCommand,
}

// ---------- 实现 ----------

func execListFiles(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	_ = json.Unmarshal(args, &in)
	base, err := env.resolveRead(in.Path)
	if err != nil {
		return "", err
	}
	entries := []string{}
	if !in.Recursive {
		des, err := os.ReadDir(base)
		if err != nil {
			return "", err
		}
		for _, de := range des {
			if len(entries) >= maxListEntries {
				break
			}
			name := de.Name()
			if de.IsDir() {
				name += "/"
			}
			entries = append(entries, name)
		}
	} else {
		root := base
		err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 单个不可读路径跳过
			}
			// 目录条目也计入上限，避免大仓库/node_modules 把内存撑爆
			if len(entries) >= maxListEntries {
				return fs.SkipAll
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			if rel == "." {
				return nil
			}
			if d.IsDir() {
				if strings.Count(rel, string(filepath.Separator)) >= 4 {
					return fs.SkipDir
				}
				entries = append(entries, filepath.ToSlash(rel)+"/")
				return nil
			}
			entries = append(entries, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if len(entries) == 0 {
		return "(空目录)", nil
	}
	sort.Strings(entries)
	if len(entries) > maxListEntries {
		entries = append(entries[:maxListEntries], fmt.Sprintf("…（超出 %d 条上限，已截断）", maxListEntries))
	}
	return strings.Join(entries, "\n"), nil
}

func execReadFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Path == "" {
		return "", fmt.Errorf("需要 path 参数")
	}
	abs, err := env.resolveRead(in.Path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 先限量读入，避免一次 os.ReadFile 把超大文件整个塞进内存。
	// 4MB 足够覆盖正常源码/文档；更大的文件明确报错，避免 OOM。
	const maxSourceBytes = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(f, maxSourceBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxSourceBytes {
		return "", fmt.Errorf("文件超过 %dMB，无法按文本读取（可先用 search_files 定位关键内容）", maxSourceBytes>>20)
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%s 是二进制文件，无法按文本读取", in.Path)
	}
	if len(raw) > maxReadBytes && in.Limit <= 0 {
		raw = []byte(truncateBytes(string(raw), maxReadBytes))
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	start := in.Offset
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return "(offset 超出文件行数)", nil
	}
	end := len(lines)
	if in.Limit > 0 && start-1+in.Limit < end {
		end = start - 1 + in.Limit
	}
	sel := lines[start-1 : end]
	out := strings.Join(sel, "\n")
	if len(out) > maxReadBytes {
		out = truncateBytes(out, maxReadBytes) + "\n…(已按 64KB 截断)"
	}
	header := fmt.Sprintf("(第 %d-%d 行，共 %d 行)\n", start, end, len(lines))
	return header + out, nil
}

func execWriteFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", fmt.Errorf("需要 path 参数")
	}
	if len(in.Content) > maxWriteBytes {
		return "", fmt.Errorf("内容超过 %dKB 上限", maxWriteBytes/1024)
	}
	abs, err := env.resolveWrite(in.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", filepath.ToSlash(in.Path), len(in.Content)), nil
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, "dist": true,
	"build": true, "__pycache__": true, ".venv": true, "venv": true, ".idea": true,
}

// truncateRunes 按 Unicode 字符数截断（避免把中文切半个）。
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// truncateBytes 按字节数截断，但不会切碎 UTF-8 序列。
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !isUTF8Boundary(s) {
		s = s[:len(s)-1]
	}
	return s
}

func isUTF8Boundary(s string) bool {
	b := s[len(s)-1]
	// 末字节若是 continuation（10xxxxxx）则可能切在字符中间
	return b&0xC0 != 0x80
}

func execSearchFiles(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("需要 query 参数")
	}
	base, err := env.resolveRead(in.Path)
	if err != nil {
		return "", err
	}
	q := strings.ToLower(in.Query)
	hits := []string{}
	scanned := 0
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(hits) >= maxSearchHits {
			return fs.SkipAll
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		scanned++
		if scanned > maxSearchFiles {
			return fs.SkipAll
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > 1024*1024 {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil || bytes.IndexByte(raw, 0) >= 0 {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				trimmed := strings.TrimSpace(line)
				if len([]rune(trimmed)) > 200 {
					trimmed = truncateRunes(trimmed, 200) + "…"
				}
				hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, trimmed))
				if len(hits) >= maxSearchHits {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("未找到包含 %q 的内容（扫描 %d 个文件）", in.Query, scanned), nil
	}
	out := strings.Join(hits, "\n")
	if len(hits) >= maxSearchHits {
		out += fmt.Sprintf("\n…（已达 %d 条上限）", maxSearchHits)
	}
	return out, nil
}

func execRunCommand(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Cwd     string   `json:"cwd"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Command == "" {
		return "", fmt.Errorf("需要 command 参数")
	}
	// 白名单校验（按 basename，兼容 git.exe / 绝对路径写法）
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(in.Command), ".exe"))
	allowed := false
	for _, a := range env.Allowlist {
		if strings.TrimSpace(a) != "" && base == strings.ToLower(strings.TrimSpace(a)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("命令 %q 不在白名单内（允许：%s）", in.Command, strings.Join(env.Allowlist, ", "))
	}
	if strings.TrimSpace(env.Workspace) == "" {
		return "", fmt.Errorf("尚未选择项目：命令只能在项目工作区内执行")
	}
	// cwd 缺省 = 项目根（与工具描述一致）；显式 cwd 必须落在工作区内
	var cwd string
	if strings.TrimSpace(in.Cwd) == "" {
		cwd = strings.TrimSpace(env.Workspace)
	} else {
		var err error
		cwd, err = env.resolveWrite(in.Cwd)
		if err != nil {
			return "", err
		}
	}
	// 目录不存在时 cmd.Run 只会把 chdir 错误拼进输出并返回 nil，
	// 上层会误判为成功——这里先校验，明确报错。
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return "", fmt.Errorf("工作目录不存在：%s（请先在侧栏选择或重建项目）", cwd)
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, in.Command, in.Args...)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := buf.String()
	if len(out) > maxOutBytes {
		out = truncateBytes(out, maxOutBytes) + "\n…(输出已按 8KB 截断)"
	}
	head := fmt.Sprintf("$ %s %s\n", in.Command, strings.Join(in.Args, " "))
	if runErr != nil {
		// 非 0 退出必须作为错误返回，否则前端/模型会把失败当成功
		return head + out, fmt.Errorf("命令退出码非 0: %w", runErr)
	}
	if strings.TrimSpace(out) == "" {
		return head + "(无输出)", nil
	}
	return head + out, nil
}
