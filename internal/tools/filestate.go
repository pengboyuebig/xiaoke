// 文件新鲜度状态戳（对标主流 agent 的 FILE_NOT_READ / STALE_FILE 护栏）：
// 会话内记录「这个文件读到过什么」，edit_file / write_file 修改已有文件前强制
// 校验「先读过 + 文件没被外部（用户/其他进程）改过」——防模型凭记忆盲写覆盖。
package tools

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// FileStamp 是一次读取留下的文件状态。
type FileStamp struct {
	MtimeMs int64 // 修改时间（毫秒，整数化比较避免亚毫秒误报）
	Size    int64
	Offset  int  // 本次读取的视图（0 = 全文）
	Limit   int  // 0 = 无 limit
	Partial bool // 视图不是全文（用过 offset/limit）→ 禁止编辑
}

// FileStateTracker 是会话级的「已读文件」登记表（每次请求构造一个，
// 同请求内的子代理共享——子代理读过的文件父代理也能改）。
type FileStateTracker struct {
	mu    sync.Mutex
	files map[string]FileStamp // key = 绝对路径
}

// NewFileStateTracker 创建空表。
func NewFileStateTracker() *FileStateTracker {
	return &FileStateTracker{files: map[string]FileStamp{}}
}

// Observe 记录一次读取（read_file 成功后调用）。
// 返回 true 表示「与上次完全同视图且文件未变」——调用方可以直接返回存根省 token。
func (t *FileStateTracker) Observe(abs string, info os.FileInfo, offset, limit int) (unchanged bool) {
	if t == nil || info == nil {
		return false
	}
	stamp := FileStamp{
		MtimeMs: info.ModTime().UnixMilli(),
		Size:    info.Size(),
		Offset:  offset,
		Limit:   limit,
		Partial: offset > 1 || limit > 0,
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.files[abs]; ok && prev == stamp {
		return true
	}
	t.files[abs] = stamp
	return false
}

// MarkEdited 写成功后刷新状态戳（文件此刻的真实状态），后续编辑以它为基准。
func (t *FileStateTracker) MarkEdited(abs string) {
	if t == nil {
		return
	}
	if info, err := os.Stat(abs); err == nil {
		t.mu.Lock()
		t.files[abs] = FileStamp{
			MtimeMs: info.ModTime().UnixMilli(),
			Size:    info.Size(),
		}
		t.mu.Unlock()
	}
}

// CheckEditable 校验「可安全修改」：读过全文 + 未被外部修改。
// 错误信息直接写行动指引（先 read_file），模型一轮即可自纠。
func (t *FileStateTracker) CheckEditable(abs string) error {
	if t == nil {
		return nil // 未启用状态跟踪（测试/旧路径），不拦截
	}
	t.mu.Lock()
	prev, ok := t.files[abs]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("文件尚未读取过：先用 read_file 读取 %s 的当前内容，再执行修改（防止凭记忆覆盖他人改动）", abs)
	}
	if prev.Partial {
		return fmt.Errorf("上次只读取了该文件的一部分（offset/limit 视图）：编辑前请不带 offset/limit 重新 read_file 全文")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("文件已不存在：%s", abs)
	}
	cur := FileStamp{MtimeMs: info.ModTime().UnixMilli(), Size: info.Size()}
	if cur.MtimeMs != prev.MtimeMs || cur.Size != prev.Size {
		return fmt.Errorf("文件在读取后被外部修改过（时间戳/大小已变化）：请重新 read_file 核对最新内容后再编辑")
	}
	return nil
}

// ChangedSince 编辑工具写回后的便捷校验（测试用）。
func (t *FileStateTracker) ChangedSince(abs string, d time.Duration) bool {
	t.mu.Lock()
	prev, ok := t.files[abs]
	t.mu.Unlock()
	if !ok {
		return false
	}
	info, err := os.Stat(abs)
	if err != nil {
		return true
	}
	return info.ModTime().Sub(time.UnixMilli(prev.MtimeMs)) > d
}
