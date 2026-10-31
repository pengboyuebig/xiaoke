// Package store 持久化聊天记录：
//
//	<id>.json       快照（含最近一次合并的所有消息）
//	<id>.log        追加日志（每条新消息一行 JSON，O(1) 追加）
//	<id>.meta.json  列表摘要（标题/绑定/消息数/更新时间，侧栏只读它）
//
// 追加消息只写一行日志 + 小体积 meta；日志超过阈值时合并回快照并清空。
// 这样既保留「可直接查看的 JSON」的优点，又避免每条消息重写整个会话。
package store

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("session not found")

var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// 日志超过该字节数就合并进快照。变量形式便于测试调小。
var logCompactBytes int64 = 1 << 20

const maxLogLine = 1 << 20

type Message struct {
	Role      string    `json:"role"` // "user" | "assistant" | "tool"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	// 隐藏消息：仍参与模型上下文，但前端不渲染（如「批准计划」的自动指令）
	Hidden bool `json:"hidden,omitempty"`
	// assistant 消息携带的工具调用（content 可为空说明文字）
	ToolCalls []ToolCallRecord `json:"toolCalls,omitempty"`
	// role=tool 时：这条结果对应的调用 ID 与工具名
	ToolCallID string `json:"toolCallId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolCallRecord 落库的助手侧工具调用（与 OpenAI tool_calls 同构）。
type ToolCallRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Todo 是会话级待办（todo_write 工具维护），随会话持久化、切会话还原。
type Todo struct {
	Content   string    `json:"content"`
	Status    string    `json:"status"` // pending | in_progress | completed
	UpdatedAt time.Time `json:"updatedAt"`
}

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ProjectID string    `json:"projectId,omitempty"`
	AgentID   string    `json:"agentId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LastSeq 是快照已包含到的日志序号（日志合并后写入）
	LastSeq  int64     `json:"lastSeq,omitempty"`
	Messages []Message `json:"messages"`

	// Summary 是主动压缩生成的「历史摘要」；SummaryUpTo 之前的消息已被它覆盖。
	// 摘要存在快照里（随快照合并/迁移一起走），发送上下文时用摘要替代旧消息。
	Summary     string    `json:"summary,omitempty"`
	SummaryUpTo time.Time `json:"summaryUpTo,omitempty"`

	// Todos 当前待办清单（整表替换式更新），压缩后仍注入上下文，长任务不丢进度。
	Todos []Todo `json:"todos,omitempty"`

	// PromptTokens/CompletionTokens/Requests 是会话累计用量（优先取上游真实
	// usage，缺失时按字节估算兜底），仅作展示，不参与压缩触发。
	PromptTokens     int64 `json:"promptTokens,omitempty"`
	CompletionTokens int64 `json:"completionTokens,omitempty"`
	Requests         int64 `json:"requests,omitempty"`
	// LastPromptTokens 是上一次请求的真实 prompt tokens（usage 缺失时为估算值）。
	// 配置了 ContextWindow 时，压缩触发优先用它——比 bytes/4 估算更贴近上游真实约束。
	LastPromptTokens int64 `json:"lastPromptTokens,omitempty"`
}

// Meta 是会话列表里展示的摘要（不含消息体）。
type Meta struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ProjectID    string    `json:"projectId,omitempty"`
	AgentID      string    `json:"agentId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
	// LastSeq 是已追加到的日志序号（用于保证序号单调）
	LastSeq int64 `json:"lastSeq,omitempty"`
}

// logEntry 是追加日志的一行。
type logEntry struct {
	Seq int64   `json:"seq"`
	Msg Message `json:"msg"`
}

type Store struct {
	dir string
	mu  sync.Mutex

	// backupMu 串行化写备份文件（否则并发备份时清理临时文件会误删对方正在写的 .tmp）
	backupMu sync.Mutex

	lockMapMu   sync.Mutex
	sessionLock map[string]*sync.Mutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, sessionLock: map[string]*sync.Mutex{}}, nil
}

// TryLockSession 尝试独占一个会话（同一会话同时只允许一个对话请求）。
// 非法/空 ID 不参与锁（新会话由服务端创建，ID 唯一），直接返回可用。
func (s *Store) TryLockSession(id string) (unlock func(), ok bool) {
	if !validID.MatchString(id) {
		return func() {}, true
	}
	s.lockMapMu.Lock()
	l := s.sessionLock[id]
	if l == nil {
		l = &sync.Mutex{}
		s.sessionLock[id] = l
	}
	s.lockMapMu.Unlock()
	if !l.TryLock() {
		return nil, false
	}
	return l.Unlock, true
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// nextSeq 返回进程内单调递增的纳秒序号（时钟回拨时也比上一次大）。
var seqCounter atomic.Int64

func nextSeq() int64 {
	for {
		now := time.Now().UnixNano()
		old := seqCounter.Load()
		if now <= old {
			now = old + 1
		}
		if seqCounter.CompareAndSwap(old, now) {
			return now
		}
	}
}

func (s *Store) path(id string) string     { return filepath.Join(s.dir, id+".json") }
func (s *Store) logPath(id string) string  { return filepath.Join(s.dir, id+".log") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.dir, id+".meta.json") }

// SetAgent 把会话绑定到智能体。
func (s *Store) SetAgent(id, agentID string) error {
	if agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
	} else if err != nil {
		return err
	}
	if meta.AgentID == agentID {
		return nil
	}
	meta.AgentID = agentID
	meta.UpdatedAt = time.Now()
	return s.saveMeta(meta)
}

// Create 新建一个空会话（可绑定项目）。
func (s *Store) Create(title, projectID string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	sess := &Session{
		ID:        newID(),
		Title:     title,
		ProjectID: projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []Message{},
	}
	if err := s.saveSnapshot(sess); err != nil {
		return nil, err
	}
	if err := s.saveMeta(metaOf(sess)); err != nil {
		return nil, err
	}
	return sess, nil
}

// List 返回全部会话的摘要，按更新时间倒序。
// 优先只读 meta 文件；没有 meta 的旧会话完整加载一次并补写 meta（一次性迁移）。
func (s *Store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	metas := []Meta{}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".meta.json")
		if !validID.MatchString(id) {
			continue
		}
		// 快照不存在时忽略孤儿 meta
		if _, err := os.Stat(s.path(id)); err != nil {
			continue
		}
		m, err := s.loadMeta(id)
		if err != nil {
			continue
		}
		metas = append(metas, m)
		seen[id] = true
	}
	// 兼容没有 meta 的旧会话：完整加载一次并补写 meta（一次性迁移），
	// 否则每次列表刷新都要全量读快照+日志，会话越多侧栏越慢。
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validID.MatchString(id) || seen[id] {
			continue
		}
		sess, err := s.loadFullLocked(id)
		if err != nil {
			continue // 单个文件损坏不影响列表
		}
		_ = s.saveMeta(metaOf(sess))
		metas = append(metas, metaOf(sess))
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	return metas, nil
}

func (s *Store) Get(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadFullLocked(id)
}

// Append 向会话追加一条消息并刷新摘要。
// 消息只写一行日志；日志超过阈值时合并进快照。
func (s *Store) Append(id string, msg Message) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
		if err := s.saveMeta(meta); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	seq := nextSeq()
	if seq <= meta.LastSeq {
		seq = meta.LastSeq + 1
	}
	line, err := json.Marshal(logEntry{Seq: seq, Msg: msg})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	meta.LastSeq = seq
	meta.MessageCount++
	meta.UpdatedAt = time.Now()
	// 首条用户消息作为会话标题
	if meta.Title == "" && msg.Role == "user" {
		meta.Title = truncate([]rune(msg.Content), 24)
	}
	if err := s.saveMeta(meta); err != nil {
		return err
	}
	if st, err := os.Stat(s.logPath(id)); err == nil && st.Size() > logCompactBytes {
		return s.compactLocked(id)
	}
	return nil
}

// SetProject 把会话绑定到项目。
func (s *Store) SetProject(id, projectID string) error {
	if projectID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if errors.Is(err, ErrNotFound) {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		meta = metaOf(sess)
	} else if err != nil {
		return err
	}
	if meta.ProjectID == projectID {
		return nil
	}
	meta.ProjectID = projectID
	meta.UpdatedAt = time.Now()
	return s.saveMeta(meta)
}

// Delete 删除会话。删除前先把三个文件复制到 trash 目录，
// 复制失败则中止删除（宁可删不掉，也不能悄悄丢数据）。
func (s *Store) Delete(id string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if _, err := s.moveToTrashLocked(id); err != nil {
		return fmt.Errorf("删除前备份失败，已取消删除: %w", err)
	}
	s.pruneTrashLocked()
	for _, p := range []string{s.logPath(id), s.metaPath(id), s.path(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// TrashDir 是删除会话时保留副本的目录（位于会话目录下的 trash/）。
func (s *Store) TrashDir() string { return filepath.Join(s.dir, "trash") }

// maxTrashEntries 回收站目录数上限：到顶后删最旧的。
// 30 天自动清理之外的第二道保险——短时间大量删会话也不会把磁盘吃穿。
const maxTrashEntries = 50

// pruneTrashLocked 回收站条数超限时按修改时间删最旧。调用方需持有 s.mu。
func (s *Store) pruneTrashLocked() {
	entries, err := os.ReadDir(s.TrashDir())
	if err != nil || len(entries) <= maxTrashEntries {
		return
	}
	type item struct {
		name string
		mod  time.Time
	}
	items := []item{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			items = append(items, item{e.Name(), info.ModTime()})
		}
	}
	if len(items) <= maxTrashEntries {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	for i := 0; i < len(items)-maxTrashEntries; i++ {
		_ = os.RemoveAll(filepath.Join(s.TrashDir(), items[i].name))
	}
}

// moveToTrashLocked 把会话文件复制到 trash/<id>-<时间戳>/。调用方需持有 s.mu。
func (s *Store) moveToTrashLocked(id string) (string, error) {
	dst := filepath.Join(s.TrashDir(), id+"-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	copied := 0
	for _, p := range []string{s.path(id), s.logPath(id), s.metaPath(id)} {
		if err := copyFile(p, filepath.Join(dst, filepath.Base(p))); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // 日志/摘要可能本来就没有
			}
			return "", err
		}
		copied++
	}
	if copied == 0 {
		return "", fmt.Errorf("没有可备份的文件")
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// trashName 匹配回收站目录名 <id>-<YYYYMMDD>-<HHMMSS>。
var trashName = regexp.MustCompile(`^([0-9a-f]{16})-(\d{8}-\d{6})$`)

// ErrTrashNotFound 表示回收站里没有这个目录。
var ErrTrashNotFound = errors.New("回收站里没有这条记录")

// ErrSessionExists 表示同 ID 的会话仍在，不能覆盖恢复。
var ErrSessionExists = errors.New("该会话仍然存在，无需恢复")

// TrashItem 是回收站里的一条记录。
type TrashItem struct {
	Dir          string    `json:"dir"`   // 目录名（恢复/彻底删除时用）
	ID           string    `json:"id"`    // 原会话 ID
	Title        string    `json:"title"` // 快照里的标题（可能为空）
	MessageCount int       `json:"messageCount"`
	DeletedAt    time.Time `json:"deletedAt"` // 取自目录名时间戳
	SizeBytes    int64     `json:"sizeBytes"`
}

// ListTrash 列出回收站记录，按删除时间倒序（最近的在前）。
func (s *Store) ListTrash() ([]TrashItem, error) {
	entries, err := os.ReadDir(s.TrashDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []TrashItem{}, nil
		}
		return nil, err
	}
	items := []TrashItem{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := trashName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		item := TrashItem{Dir: e.Name(), ID: m[1]}
		if t, err := time.ParseInLocation("20060102-150405", m[2], time.Local); err == nil {
			item.DeletedAt = t
		}
		dir := filepath.Join(s.TrashDir(), e.Name())
		item.MessageCount = countMessagesInDir(dir, item.ID)
		if data, err := os.ReadFile(filepath.Join(dir, item.ID+".json")); err == nil {
			var snap Session
			if json.Unmarshal(data, &snap) == nil {
				item.Title = snap.Title
			}
		}
		if fs, err := os.ReadDir(dir); err == nil {
			for _, f := range fs {
				if info, err := f.Info(); err == nil {
					item.SizeBytes += info.Size()
				}
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DeletedAt.Equal(items[j].DeletedAt) {
			return items[i].Dir > items[j].Dir
		}
		return items[i].DeletedAt.After(items[j].DeletedAt)
	})
	return items, nil
}

// countMessagesInDir 统计回收站副本的消息数：快照 + 未合并日志。
// 删除时快照可能还没合并日志，只看快照会漏掉最近的消息。
func countMessagesInDir(dir, id string) int {
	n := 0
	if data, err := os.ReadFile(filepath.Join(dir, id+".json")); err == nil {
		var snap Session
		if json.Unmarshal(data, &snap) == nil {
			n = len(snap.Messages)
		}
	}
	f, err := os.Open(filepath.Join(dir, id+".log"))
	if err != nil {
		return n
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLogLine)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

// RestoreTrash 把回收站里的一条记录恢复到会话目录。
// 同 ID 会话仍存在时拒绝覆盖（返回 ErrSessionExists）。
func (s *Store) RestoreTrash(dirName string) (string, error) {
	m := trashName.FindStringSubmatch(dirName)
	if m == nil {
		return "", ErrTrashNotFound
	}
	id := m[1]
	s.mu.Lock()
	defer s.mu.Unlock()
	src := filepath.Join(s.TrashDir(), dirName)
	if _, err := os.Stat(filepath.Join(src, id+".json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrTrashNotFound
		}
		return "", err
	}
	if _, err := os.Stat(s.path(id)); err == nil {
		return "", ErrSessionExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// 快照必须成功；日志/摘要缺失不算错误
	if err := copyFile(filepath.Join(src, id+".json"), s.path(id)); err != nil {
		return "", err
	}
	for _, name := range []string{id + ".log", id + ".meta.json"} {
		if err := copyFile(filepath.Join(src, name), filepath.Join(s.dir, name)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	_ = os.RemoveAll(src)
	return id, nil
}

// PurgeTrash 彻底删除回收站里的一条记录。
func (s *Store) PurgeTrash(dirName string) error {
	if trashName.FindStringSubmatch(dirName) == nil {
		return ErrTrashNotFound
	}
	src := filepath.Join(s.TrashDir(), dirName)
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTrashNotFound
		}
		return err
	}
	return os.RemoveAll(src)
}

// PurgeTrashOlderThan 清理超过 d 的回收站记录，返回清理条数。
// d<=0 表示不清理（避免误把保留期写成 0 导致全删）。
func (s *Store) PurgeTrashOlderThan(d time.Duration) (int, error) {
	if d <= 0 {
		return 0, nil
	}
	items, err := s.ListTrash()
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-d)
	n := 0
	for _, it := range items {
		if it.DeletedAt.IsZero() || it.DeletedAt.After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.TrashDir(), it.Dir)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Export 返回完整会话的 JSON（快照 + 未合并日志），用于单会话导出。
func (s *Store) Export(id string) ([]byte, error) {
	sess, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(sess, "", "  ")
}

// backupManifest 是整包备份里的说明文件，便于人工核对。
type backupManifest struct {
	CreatedAt time.Time `json:"createdAt"`
	Count     int       `json:"count"`
	Sessions  []string  `json:"sessions"`
}

// WriteBackup 把全部会话文件打包成 zip 写到 w（不包含 trash/）。
func (s *Store) WriteBackup(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		id := ""
		switch {
		case strings.HasSuffix(name, ".meta.json"):
			id = strings.TrimSuffix(name, ".meta.json")
		case strings.HasSuffix(name, ".json"):
			id = strings.TrimSuffix(name, ".json")
		case strings.HasSuffix(name, ".log"):
			id = strings.TrimSuffix(name, ".log")
		}
		if !validID.MatchString(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return err
		}
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
	}
	ids := s.listIDsLocked(entries)
	manifest, err := json.MarshalIndent(backupManifest{
		CreatedAt: time.Now(), Count: len(ids), Sessions: ids,
	}, "", "  ")
	if err != nil {
		return err
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	if _, err := mw.Write(manifest); err != nil {
		return err
	}
	return zw.Close()
}

// listIDsLocked 列出目录里所有合法会话 ID（快照存在才算）。调用方需持有 s.mu。
func (s *Store) listIDsLocked(entries []os.DirEntry) []string {
	ids := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if validID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Backup 在 dir 下生成 sessions-<时间戳>.zip，并只保留最近 keep 份（keep<=0 表示不清理）。
func (s *Store) Backup(dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	cleanStaleTemps(dir) // 上次进程被强杀可能留下半截 .tmp
	name := "sessions-" + time.Now().Format("20060102-150405") + ".zip"
	path := filepath.Join(dir, name)
	// 同一秒内重复备份时避免覆盖
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("sessions-%s-%d.zip", time.Now().Format("20060102-150405"), i))
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if err := s.WriteBackup(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if keep > 0 {
		_ = pruneBackups(dir, keep) // 清理失败不影响本次备份
	}
	return path, nil
}

// cleanStaleTemps 清理上次被强杀留下的半截备份临时文件。
func cleanStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "sessions-") || !strings.HasSuffix(n, ".zip.tmp") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// pruneBackups 只保留最近 keep 份备份（按文件名里的时间戳排序）。
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "sessions-") || !strings.HasSuffix(n, ".zip") {
			continue
		}
		names = append(names, n)
	}
	if len(names) <= keep {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, n := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// loadFullLocked 读取快照 + 未合并的日志，并应用 meta 头字段。调用方需持有 s.mu。
func (s *Store) loadFullLocked(id string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	sess, err := s.loadSnapshot(id)
	if err != nil {
		return nil, err
	}
	snapshotSeq := sess.LastSeq
	if m, err := s.loadMeta(id); err == nil {
		sess.Title = m.Title
		sess.ProjectID = m.ProjectID
		sess.AgentID = m.AgentID
		if !m.CreatedAt.IsZero() {
			sess.CreatedAt = m.CreatedAt
		}
		if !m.UpdatedAt.IsZero() {
			sess.UpdatedAt = m.UpdatedAt
		}
		if m.LastSeq > sess.LastSeq {
			sess.LastSeq = m.LastSeq
		}
	}
	entries, err := s.loadLog(id)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Seq <= snapshotSeq {
			continue // 已合并进快照
		}
		sess.Messages = append(sess.Messages, e.Msg)
		if e.Seq > sess.LastSeq {
			sess.LastSeq = e.Seq
		}
	}
	return sess, nil
}

func (s *Store) loadSnapshot(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("会话 %s 损坏: %w", id, err)
	}
	if sess.ID != id {
		return nil, fmt.Errorf("会话 %s 内容与文件名不一致", id)
	}
	return &sess, nil
}

// loadLog 读取追加日志；半截/损坏的行跳过（进程崩溃时可能出现）。
func (s *Store) loadLog(id string) ([]logEntry, error) {
	f, err := os.Open(s.logPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxLogLine)
	out := []logEntry{}
	seen := map[int64]bool{}
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // 半截/坏行
		}
		if e.Seq <= 0 || seen[e.Seq] {
			continue
		}
		seen[e.Seq] = true
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// compactLocked 把快照 + 日志合并成新快照，并清空日志。调用方需持有 s.mu。
func (s *Store) compactLocked(id string) error {
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	// 删除失败不阻断：快照的 LastSeq 会让旧日志条目在读取时被跳过
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

// SetTitle 设置会话标题（写 meta 即可：加载侧以 meta 为准）。
// 已有标题时不覆盖——自动命名只补空白，用户改过就尊重。
func (s *Store) SetTitle(id, title string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if rs := []rune(title); len(rs) > 32 {
		title = string(rs[:32])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadMeta(id)
	if err != nil {
		// 无 meta 的旧会话：先补一份再改
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		m = metaOf(sess)
	}
	if strings.TrimSpace(m.Title) != "" {
		return nil
	}
	m.Title = title
	m.UpdatedAt = time.Now()
	return s.saveMeta(m)
}

// RenameTitle 强制重命名会话（与 SetTitle 的「只补空白」不同，这是用户主动改名）。
// 允许改名后跟随自动命名：传空串表示清空标题，下次对话会重新自动命名。
func (s *Store) RenameTitle(id, title string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	title = strings.TrimSpace(title)
	if rs := []rune(title); len(rs) > 64 {
		title = string(rs[:64])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadMeta(id)
	if err != nil {
		sess, lerr := s.loadFullLocked(id)
		if lerr != nil {
			return lerr
		}
		m = metaOf(sess)
	}
	m.Title = title
	m.UpdatedAt = time.Now()
	return s.saveMeta(m)
}

// SetSummary 持久化「历史摘要」：把摘要写进快照并收敛日志，
// 保证重启后摘要与消息边界（SummaryUpTo）一起还原。
func (s *Store) SetSummary(id string, summary string, upTo time.Time) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	sess.Summary = summary
	sess.SummaryUpTo = upTo
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	// 消息体已随快照落盘（导出/回收站仍能看到完整历史），日志收敛掉避免双份；
	// 读取侧按 SummaryUpTo 过滤摘要覆盖范围。
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

// SetTodos 整表替换会话待办（todo_write 工具的持久化入口）。
func (s *Store) SetTodos(id string, todos []Todo) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	sess.Todos = todos
	sess.UpdatedAt = time.Now()
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

// AddUsage 累计会话用量（每次对话请求结束时调用一次）。
// lastPrompt 同时覆盖 LastPromptTokens（压缩触发优先用这个真实值）。
func (s *Store) AddUsage(id string, prompt, completion int64) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.loadFullLocked(id)
	if err != nil {
		return err
	}
	sess.PromptTokens += prompt
	sess.CompletionTokens += completion
	sess.Requests++
	sess.LastPromptTokens = prompt
	if err := s.saveSnapshot(sess); err != nil {
		return err
	}
	_ = os.Remove(s.logPath(id))
	return s.saveMeta(metaOf(sess))
}

func (s *Store) saveSnapshot(sess *Session) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(sess.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sess.ID))
}

func (s *Store) saveMeta(m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath(m.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath(m.ID))
}

func (s *Store) loadMeta(id string) (Meta, error) {
	data, err := os.ReadFile(s.metaPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil || m.ID != id {
		return Meta{}, ErrNotFound
	}
	return m, nil
}

func metaOf(sess *Session) Meta {
	return Meta{
		ID: sess.ID, Title: sess.Title, ProjectID: sess.ProjectID, AgentID: sess.AgentID,
		CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt,
		MessageCount: len(sess.Messages), LastSeq: sess.LastSeq,
	}
}

func truncate(rs []rune, n int) string {
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
