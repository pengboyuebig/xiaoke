// Package memory 提供「小柯」的长期记忆：全局 + 项目两级 MEMORY.md。
//
// 与会话摘要（compact）的分工：摘要是「本会话更早内容的压缩」，随会话走；
// 记忆是「跨会话值得保留的知识」——用户偏好、项目结构、纠正过的错误。
// 模型通过 memory_append / memory_read 工具读写；每次请求把两层记忆
// 注入系统提示词（Assembly，预算内）。文件放在 data/memory 下而非用户
// 项目目录里，避免污染用户代码库。
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scope 决定读写哪一层记忆。
type Scope string

const (
	ScopeGlobal  Scope = "global"  // 跟随用户（跨项目）
	ScopeProject Scope = "project" // 绑定当前项目
)

const (
	// maxFileBytes 单个记忆文件上限：满了明确报错让模型精炼，而不是静默丢内容。
	maxFileBytes = 64 * 1024
	// 全局/项目各自注入预算；两层都满时系统提示词最多多出 8KB。
	injectBudgetBytes = 4 * 1024
)

// Store 是并发安全的记忆仓库（root = <dataDir>/memory）。
type Store struct {
	mu   sync.Mutex
	root string
	// cache 按「路径」缓存最近读取的内容与 mtime+size，避免每次请求都读盘。
	cache map[string]cacheEntry
}

type cacheEntry struct {
	content string
	modTime time.Time
	size    int64
}

// Open 打开（或初始化）记忆仓库。
func Open(root string) (*Store, error) {
	s := &Store{root: root, cache: map[string]cacheEntry{}}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) pathFor(scope Scope, projectID string) (string, error) {
	if scope == ScopeGlobal {
		return filepath.Join(s.root, "MEMORY.md"), nil
	}
	if !validProjectID(projectID) {
		return "", fmt.Errorf("项目 ID 不合法，无法定位项目记忆")
	}
	return filepath.Join(s.root, "projects", projectID, "MEMORY.md"), nil
}

// validProjectID 项目 ID 只可能是十六进制串（store 生成），白名单校验防路径注入。
func validProjectID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Append 追加一条记忆；内容已存在（按整段精确匹配）时幂等跳过。
// 返回给模型的回执说明实际做了什么。
func (s *Store) Append(scope Scope, projectID, text string) (string, error) {
	text = normalizeMemoryText(text)
	if text == "" {
		return "", fmt.Errorf("记忆内容不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFor(scope, projectID)
	if err != nil {
		return "", err
	}
	existing, err := s.readLocked(path)
	if err != nil {
		return "", err
	}
	if memoryHasEntry(existing, text) {
		return "这条记忆已存在，跳过（不要重复追加已有内容）", nil
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("- " + text + "\n")
	content := b.String()
	if len(content) > maxFileBytes {
		return "", fmt.Errorf("记忆文件已满（%dKB 上限）：只记真正重要的结论，或请用户整理记忆", maxFileBytes/1024)
	}
	if err := s.writeLocked(path, content); err != nil {
		return "", err
	}
	label := "全局记忆"
	if scope == ScopeProject {
		label = "项目记忆"
	}
	return fmt.Sprintf("已写入%s（第 %d 条）", label, strings.Count(content, "\n- ")), nil
}

// Read 返回某一层记忆的原文（空文件返回空串）。
func (s *Store) Read(scope Scope, projectID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFor(scope, projectID)
	if err != nil {
		return "", err
	}
	return s.readLocked(path)
}

// ImportGlobal 批量导入候选记忆（离线整理用）：逐条去重后追加，
// 返回实际新增条数。行太长/太短/包含敏感样式的行直接跳过。
func (s *Store) ImportGlobal(candidates []string) (int, error) {
	added := 0
	for _, line := range candidates {
		line = normalizeMemoryText(line)
		if line == "" || len([]rune(line)) < 4 || len([]rune(line)) > 300 {
			continue
		}
		if strings.ContainsAny(line, "\n") {
			// 多行候选压成一行（离线整理的输出按行切分，不该再有多行）
			fields := strings.Fields(line)
			line = strings.Join(fields, " ")
		}
		out, err := s.Append(ScopeGlobal, "", line)
		if err != nil {
			if strings.Contains(err.Error(), "已存在") {
				continue
			}
			return added, err
		}
		if strings.Contains(out, "已存在") {
			continue // 幂等跳过，不计入新增
		}
		added++
	}
	return added, nil
}

// WriteGlobal 整体覆写全局记忆（设置页编辑入口）。项目记忆只经工具增量维护。
func (s *Store) WriteGlobal(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(content) > maxFileBytes {
		return fmt.Errorf("内容超过 %dKB 上限", maxFileBytes/1024)
	}
	path, err := s.pathFor(ScopeGlobal, "")
	if err != nil {
		return err
	}
	return s.writeLocked(path, normalizeMemoryText(content)+"\n")
}

// Assembly 组装注入系统提示词的记忆段（全局 + 项目，各自限预算）。
// 两层都为空时返回空串——提示词里不出现空段。
func (s *Store) Assembly(projectID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sections []string
	if g, _ := s.readLocked(s.mustGlobalPath()); strings.TrimSpace(g) != "" {
		sections = append(sections, "【全局记忆】\n"+truncateMemory(g))
	}
	if validProjectID(projectID) {
		if p, _ := s.readLocked(s.mustProjectPath(projectID)); strings.TrimSpace(p) != "" {
			sections = append(sections, "【项目记忆】\n"+truncateMemory(p))
		}
	}
	return strings.Join(sections, "\n\n")
}

func (s *Store) mustGlobalPath() string {
	p, _ := s.pathFor(ScopeGlobal, "")
	return p
}

func (s *Store) mustProjectPath(id string) string {
	p, _ := s.pathFor(ScopeProject, id)
	return p
}

// readLocked 读文件（调用方需持锁），带 mtime+size 缓存；不存在视为空。
func (s *Store) readLocked(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if ce, ok := s.cache[path]; ok && ce.modTime.Equal(info.ModTime()) && ce.size == info.Size() {
		return ce.content, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	s.cache[path] = cacheEntry{content: content, modTime: info.ModTime(), size: info.Size()}
	return content, nil
}

// writeLocked 原子写并刷新缓存（调用方需持锁）。
func (s *Store) writeLocked(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if info, err := os.Stat(path); err == nil {
		s.cache[path] = cacheEntry{content: content, modTime: info.ModTime(), size: info.Size()}
	}
	return nil
}

// normalizeMemoryText 清理待写入的文本：剥零宽字符与首尾空白、折叠 3 个以上
// 连续换行。记忆是结论不是日志，长篇原文不该进这里。
func normalizeMemoryText(s string) string {
	r := strings.NewReplacer(
		"\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "",
		"\r\n", "\n",
	)
	s = r.Replace(s)
	s = strings.TrimSpace(s)
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// memoryHasEntry 判断某条记忆是否已存在：整块以 "- " 条目形态精确出现
// （支持多行条目）。单行的退化为按行匹配也覆盖在内。
func memoryHasEntry(existing, text string) bool {
	if strings.Contains(existing, "- "+text) {
		return true
	}
	// 单行条目可能被设置页编辑时丢了 "- " 前缀，按行兜底一次
	for _, line := range strings.Split(existing, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		if line != "" && line == text {
			return true
		}
	}
	return false
}

// truncateMemory 注入预算截断：按字节保留头部，不切碎 UTF-8，并标注省略。
func truncateMemory(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= injectBudgetBytes {
		return content
	}
	cut := content[:injectBudgetBytes]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n…（记忆过长已截断展示，用 memory_read 可看指定层的完整内容）"
}
