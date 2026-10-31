// Package rules 读取项目规则文件（AGENTS.md / CLAUDE.md 等）并组装成
// 可注入系统提示词的规则段。
//
// 对齐主流 agent 的共识：规则文件让「项目约定跟着仓库走」——构建命令、
// 代码风格、目录结构不用每次在对话里重复。只认根目录下的命名文件
// （小柯的「当前目录」恒为项目根，无需像 CLI agent 那样沿 cwd→git root 链逐层发现）。
//
// 安全：规则文件内容属于第三方输入，注入前必须中和伪框架标签
// （</?system-reminder>、</?rules> 等），防止规则文件伪装成系统指令。
package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// maxFileBytes 单文件上限；总量上限兜底多个规则文件叠加。
	maxFileBytes   = 16 * 1024
	maxTotalBytes  = 32 * 1024
	maxFilesPerDir = 8
)

// ruleNames 是按优先级排列的候选文件名（同目录命中多个时取前者）。
var ruleNames = []string{
	"AGENTS.md", "Agents.md", "AGENT.md",
	"CLAUDE.md", "CLAUDE.local.md",
}

// fakeTagPattern 匹配伪框架标签（任意大小写、允许标签内空白变形），
// 命中即把前导 `<` 转义，使其无法闭合/伪造包裹。
var fakeTagPattern = regexp.MustCompile(`(?i)<(\s*/?\s*(?:system[-_]reminder|rules?|system\b))`)

// RuleFile 是一个已读取并转义过的规则文件。
type RuleFile struct {
	Path    string // 绝对路径（注入时展示来源）
	Content string // 已截断、已转义
}

// Store 是带缓存的规则读取器（多请求共享）。
type Store struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	files   []RuleFile
	modTime time.Time
	size    int64
	// hit 记录缓存是否有效（目录下一个文件都没有时也缓存空结果）
	hit bool
}

func New() *Store {
	return &Store{cache: map[string]cacheEntry{}}
}

// Discover 扫描 roots（项目工作区 + 只读授权目录）下根级规则文件。
// 返回拼接好的注入文本；没有任何规则文件时返回空串。
func (s *Store) Discover(roots []string) string {
	var b strings.Builder
	total := 0
	count := 0
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for _, f := range s.discoverRoot(root) {
			if count >= maxFilesPerDir*len(roots) || total >= maxTotalBytes {
				b.WriteString("\n\n（规则文件过多，已省略后续内容）")
				return strings.TrimSpace(b.String())
			}
			if total+len(f.Content) > maxTotalBytes {
				f.Content = truncateRunes(f.Content, maxTotalBytes-total) + "\n…（因总量上限截断）"
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("## 来自 " + filepath.ToSlash(f.Path) + "\n" + f.Content)
			total += len(f.Content)
			count++
		}
	}
	return strings.TrimSpace(b.String())
}

// discoverRoot 扫描单个根目录，带目录级缓存。
func (s *Store) discoverRoot(root string) []RuleFile {
	var names []string
	var modTime time.Time
	var size int64
	seen := map[string]bool{}
	for _, name := range ruleNames {
		// Windows 等大小写不敏感文件系统上 AGENTS.md 与 Agents.md 是同一个文件：
		// 按小写名去重，优先级高的先占位
		lower := strings.ToLower(name)
		if seen[lower] {
			continue
		}
		p := filepath.Join(root, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			seen[lower] = true
			names = append(names, name)
			if info.ModTime().After(modTime) {
				modTime = info.ModTime()
			}
			size += info.Size()
		}
	}
	if len(names) == 0 {
		return nil
	}

	key := root
	s.mu.Lock()
	if ce, ok := s.cache[key]; ok && ce.hit && ce.modTime.Equal(modTime) && ce.size == size {
		s.mu.Unlock()
		return ce.files
	}
	s.mu.Unlock()

	files := make([]RuleFile, 0, len(names))
	for _, name := range names {
		p := filepath.Join(root, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content := sanitize(string(data))
		if strings.TrimSpace(content) == "" {
			continue
		}
		if len(content) > maxFileBytes {
			content = truncateRunes(content, maxFileBytes) + "\n…（因单文件上限截断）"
		}
		files = append(files, RuleFile{Path: p, Content: content})
	}
	if len(files) == 0 {
		files = nil
	}

	s.mu.Lock()
	s.cache[key] = cacheEntry{files: files, modTime: modTime, size: size, hit: true}
	s.mu.Unlock()
	return files
}

// sanitize 中和规则内容里的伪框架标签：把匹配到的前导 `<` 转成 `&lt;`，
// 使 `</system-reminder>`、`<rules>`、`<SYSTEM_REMINDER>` 等无法闭合或伪造
// 包裹结构（规则是项目约定，不是系统指令，不能伪装成框架消息）。
func sanitize(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return fakeTagPattern.ReplaceAllString(content, "&lt;$1")
}

func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
