// 会话回退：「回到某条用户消息重来」。
//
// 原理：write_file/edit_file 每次成功落盘后，把「写前内容」记入该会话的
// 改动台账（data/revert/<sessionID>.json）。回退 = 台账中消息边界之后的条目
// 逆序恢复（有写前内容则写回，新建文件则删除），再截断该消息之后的所有消息。
// 简单、可靠、不依赖 git——项目不是 git 仓库也能回滚。
package chat

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// revertEntry 是一条文件改动记录。
type revertEntry struct {
	Path     string    `json:"path"`     // 相对项目根的显示路径（给人看）
	Abs      string    `json:"abs"`      // 绝对路径（回滚直接用）
	Before   string    `json:"before"`   // 写前内容；Existed=false 时为空
	Existed  bool      `json:"existed"`  // 写前文件是否存在（false = 新建，回滚即删除）
	MsgCount int       `json:"msgCount"` // 改动发生时的消息数（定位消息边界）
	At       time.Time `json:"at"`
}

const (
	revertMaxEntries = 256      // 单会话台账条数上限（超限丢最旧）
	revertMaxFile    = 48 << 20 // 台账文件字节上限（before 内容是大头）
)

// revertJournal 管理台账文件的读写。
type revertJournal struct {
	mu  sync.Mutex
	dir string
}

func newRevertJournal(dir string) *revertJournal {
	return &revertJournal{dir: dir}
}

func (r *revertJournal) path(sessionID string) string {
	return filepath.Join(r.dir, sessionID+".json")
}

// load 读出台账（文件不存在/损坏 = 空台账：回退不可用好过回退到错误内容）。
func (r *revertJournal) load(sessionID string) []revertEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(sessionID)
}

// loadLocked 读出台账。调用方需持有 r.mu。
func (r *revertJournal) loadLocked(sessionID string) []revertEntry {
	data, err := os.ReadFile(r.path(sessionID))
	if err != nil {
		return nil
	}
	var entries []revertEntry
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}
	return entries
}

// save 落盘台账；超限时从最旧开始丢（before 内容是大头）。
func (r *revertJournal) save(sessionID string, entries []revertEntry) {
	if len(entries) > revertMaxEntries {
		entries = entries[len(entries)-revertMaxEntries:]
	}
	for len(entries) > 1 {
		data, err := json.Marshal(entries)
		if err == nil && len(data) <= revertMaxFile {
			break
		}
		entries = entries[1:]
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		log.Printf("revert 台账目录创建失败: %v", err)
		return
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	tmp := r.path(sessionID) + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, r.path(sessionID))
	}
}

// Record 追加一条改动记录（写前快照 + 消息边界）。
func (r *revertJournal) Record(sessionID string, e revertEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.loadLocked(sessionID)
	r.save(sessionID, append(entries, e))
}

// RetainAfter 只保留 MsgCount <= index 的条目（与截断后剩下的消息对应）。
func (r *revertJournal) RetainAfter(sessionID string, index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.loadLocked(sessionID)
	kept := entries[:0]
	for _, e := range entries {
		if e.MsgCount <= index {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		_ = os.Remove(r.path(sessionID))
		return
	}
	r.save(sessionID, kept)
}

// RevertRequest / revertFileReport 是回退 API 的出入参。
type RevertRequest struct {
	Index int `json:"index"` // 保留到第 index 条消息（含）
}

type revertFileReport struct {
	Path   string `json:"path"`
	Action string `json:"action"` // restored | deleted | skipped
	Reason string `json:"reason,omitempty"`
}

// revertToMessage 执行回退：恢复文件 → 截断消息 → 修剪台账。
// 调用方必须已持有会话锁。
func (h *Handler) revertToMessage(sessionID string, index int) ([]revertFileReport, error) {
	entries := h.revert.load(sessionID)
	var reports []revertFileReport
	// 逆序（最新改动先恢复）：同文件多次改动时逐次覆盖，最终回到最早的写前状态
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.MsgCount <= index {
			continue // 边界之前的改动属于保留区间
		}
		if !e.Existed {
			if err := os.Remove(e.Abs); err != nil && !errors.Is(err, os.ErrNotExist) {
				reports = append(reports, revertFileReport{Path: e.Path, Action: "skipped", Reason: err.Error()})
				continue
			}
			reports = append(reports, revertFileReport{Path: e.Path, Action: "deleted"})
			continue
		}
		if e.Before == "" {
			// 写前内容缺失（原文件为空或超大未记录）：跳过，宁可少回滚不错回滚
			reports = append(reports, revertFileReport{Path: e.Path, Action: "skipped", Reason: "无写前快照"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(e.Abs), 0o755); err != nil {
			reports = append(reports, revertFileReport{Path: e.Path, Action: "skipped", Reason: err.Error()})
			continue
		}
		if err := os.WriteFile(e.Abs, []byte(e.Before), 0o644); err != nil {
			reports = append(reports, revertFileReport{Path: e.Path, Action: "skipped", Reason: err.Error()})
			continue
		}
		reports = append(reports, revertFileReport{Path: e.Path, Action: "restored"})
	}
	if err := h.store.TruncateAfter(sessionID, index); err != nil {
		return reports, err
	}
	h.revert.RetainAfter(sessionID, index)
	return reports, nil
}

// revertSessionHTTP 处理 POST /api/sessions/{id}/revert。
func (h *Handler) revertSessionHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limitBody(w, r, 4<<10)
	var req RevertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON（需要 {\"index\": N}）"})
		return
	}
	unlock, ok := h.store.TryLockSession(id)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "该会话正在生成回复，稍后再试"})
		return
	}
	defer unlock()
	// index 必须指向一条真实存在的 user 消息（回退的语义是「重问这条」）
	sess, err := h.store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if req.Index < 0 || req.Index >= len(sess.Messages) || sess.Messages[req.Index].Role != "user" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "index 必须指向一条用户消息"})
		return
	}
	reports, err := h.revertToMessage(id, req.Index)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if reports == nil {
		reports = []revertFileReport{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"index": req.Index, "files": reports})
}

// revertRecord 是 converse 里注入 env.OnFileChange 的实现：
// 记录写前快照与当前消息边界。记录失败只记日志——台账是辅助能力，不能影响写主路径。
func (h *Handler) revertRecord(sessionID, workspace, abs, before string, existed bool) {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		rel = filepath.Base(abs)
	}
	h.revert.Record(sessionID, revertEntry{
		Path:     filepath.ToSlash(rel),
		Abs:      abs,
		Before:   before,
		Existed:  existed,
		MsgCount: h.store.MessageCount(sessionID),
		At:       time.Now(),
	})
}
