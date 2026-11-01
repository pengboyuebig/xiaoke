// Package cron 提供「小柯」的定时任务（对标主流 agent 的 CronCreate 族工具）：
// 5 段标准 cron 表达式（本地时区）驱动无人值守对话。
//
// 设计：
//   - 存储：data/automations.json（与全项目 JSON 存储风格一致）
//   - 全局上限 20 条（所有状态占额，删除才释放）
//   - 防递归：自动化触发的会话内禁止再建定时任务
//   - 求值器自研（*/n、列表、区间、本地时区），零第三方依赖
package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxAutomations 是全局任务数上限。
const MaxAutomations = 20

// oncePrefix 是一次性延迟任务的表达式前缀（execCronCreate 翻译 delay_minutes 产生）。
const oncePrefix = "once:"

var (
	ErrNotFound  = errors.New("automation not found")
	ErrLimit     = errors.New("定时任务已达上限（20 条），请先删除不用的任务")
	ErrInvalid   = errors.New("cron 表达式不合法")
	ErrForbidden = errors.New("自动化触发的会话内不允许再创建定时任务（防递归）")
)

// Automation 是一条定时任务。
type Automation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Cron      string    `json:"cron"`
	Prompt    string    `json:"prompt"`
	ProjectID string    `json:"projectId,omitempty"`
	Enabled   bool      `json:"enabled"`
	MaxRuns   int       `json:"maxRuns,omitempty"` // 0 = 不限
	RunCount  int       `json:"runCount"`
	NextRunAt time.Time `json:"nextRunAt,omitempty"`
	LastRunAt time.Time `json:"lastRunAt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	LastError string    `json:"lastError,omitempty"`
}

// Store 是并发安全的定时任务仓库。
type Store struct {
	mu    sync.Mutex
	path  string
	items []Automation
}

// Open 加载（不存在则空表）。
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []Automation `json:"automations"`
	}
	if err := json.Unmarshal(data, &raw); err == nil {
		s.items = raw.Items
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		Items []Automation `json:"automations"`
	}{Items: s.items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List 返回全部任务（按下次运行时间升序）。
func (s *Store) List() []Automation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Automation, len(s.items))
	copy(out, s.items)
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	return out
}

// Get 按_id查。
func (s *Store) Get(id string) (Automation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.items {
		if a.ID == id {
			return a, true
		}
	}
	return Automation{}, false
}

// Create 新建任务：校验表达式、算首次运行时间、限流。
func (s *Store) Create(title, expr, prompt, projectID string, maxRuns int, now time.Time) (Automation, error) {
	title = strings.TrimSpace(title)
	expr = strings.TrimSpace(expr)
	prompt = strings.TrimSpace(prompt)
	if title == "" || prompt == "" {
		return Automation{}, fmt.Errorf("title 与 prompt 不能为空")
	}
	// once:N = 一次性延迟任务（execCronCreate 把 delay_minutes 翻译成这种形态）：
	// 不进 cron 求值器，直接定 NextRun = now + N 分钟，跑一次即停。
	var onceDelay time.Duration
	if strings.HasPrefix(expr, oncePrefix) {
		mins, err := strconv.Atoi(strings.TrimPrefix(expr, oncePrefix))
		if err != nil || mins < 1 || mins > 525600 {
			return Automation{}, fmt.Errorf("%w: 一次性延迟 %q 不合法（1..525600 分钟）", ErrInvalid, expr)
		}
		onceDelay = time.Duration(mins) * time.Minute
	} else {
		sched, err := Parse(expr)
		if err != nil {
			return Automation{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if sched.Next(now).IsZero() {
			return Automation{}, fmt.Errorf("%w: 表达式永远匹配不到时间", ErrInvalid)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= MaxAutomations {
		return Automation{}, ErrLimit
	}
	a := Automation{
		ID:        newID(),
		Title:     title,
		Cron:      expr,
		Prompt:    prompt,
		ProjectID: projectID,
		Enabled:   true,
		MaxRuns:   maxRuns,
		CreatedAt: now,
	}
	if onceDelay > 0 {
		a.NextRunAt = now.Add(onceDelay)
		if a.MaxRuns <= 0 {
			a.MaxRuns = 1 // 一次性任务跑一次即停
		}
	} else {
		sched, _ := Parse(expr)
		a.NextRunAt = sched.Next(now)
	}
	if a.NextRunAt.IsZero() {
		return Automation{}, fmt.Errorf("%w: 表达式永远匹配不到时间", ErrInvalid)
	}
	s.items = append(s.items, a)
	if err := s.saveLocked(); err != nil {
		return Automation{}, err
	}
	return a, nil
}

// Update 修改任务的可变字段（空值保持）。
func (s *Store) Update(id, title, expr, prompt string, maxRuns int, enabled *bool, now time.Time) (Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		a := &s.items[i]
		if a.ID != id {
			continue
		}
		if strings.TrimSpace(title) != "" {
			a.Title = strings.TrimSpace(title)
		}
		if strings.TrimSpace(prompt) != "" {
			a.Prompt = strings.TrimSpace(prompt)
		}
		if strings.TrimSpace(expr) != "" && expr != a.Cron {
			if strings.HasPrefix(expr, oncePrefix) {
				mins, err := strconv.Atoi(strings.TrimPrefix(expr, oncePrefix))
				if err != nil || mins < 1 || mins > 525600 {
					return Automation{}, fmt.Errorf("%w: 一次性延迟 %q 不合法", ErrInvalid, expr)
				}
				a.NextRunAt = now.Add(time.Duration(mins) * time.Minute)
			} else {
				sched, err := Parse(expr)
				if err != nil {
					return Automation{}, fmt.Errorf("%w: %v", ErrInvalid, err)
				}
				a.NextRunAt = sched.Next(now)
			}
			a.Cron = expr
		}
		if maxRuns >= 0 {
			a.MaxRuns = maxRuns
		}
		if enabled != nil {
			a.Enabled = *enabled
			if *enabled {
				sched, err := Parse(a.Cron)
				if err == nil {
					a.NextRunAt = sched.Next(now)
				}
			}
		}
		if err := s.saveLocked(); err != nil {
			return Automation{}, err
		}
		return *a, nil
	}
	return Automation{}, ErrNotFound
}

// Delete 删除任务。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// Due 取出「已到期且可运行」的任务：登记本次运行（NextRun 推进、计数+1、
// LastRun 更新）并落盘，调用方执行失败后再以 RecordError 记录。
// 锁内完成领取，防并发调度器重复触发。
func (s *Store) Due(now time.Time) []Automation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []Automation
	for i := range s.items {
		a := &s.items[i]
		if !a.Enabled || a.NextRunAt.IsZero() || a.NextRunAt.After(now) {
			continue
		}
		if a.MaxRuns > 0 && a.RunCount >= a.MaxRuns {
			a.Enabled = false
			continue
		}
		if strings.HasPrefix(a.Cron, oncePrefix) {
			// 一次性任务：跑完即停，无需推进下一次
			a.LastRunAt = now
			a.RunCount++
			a.NextRunAt = time.Time{}
			a.Enabled = false
			due = append(due, *a)
			continue
		}
		sched, err := Parse(a.Cron)
		if err != nil {
			a.Enabled = false
			a.LastError = "表达式失效: " + err.Error()
			continue
		}
		a.LastRunAt = now
		a.RunCount++
		a.NextRunAt = sched.Next(now)
		if a.MaxRuns > 0 && a.RunCount >= a.MaxRuns {
			a.Enabled = false // 跑满次数自动停用
		}
		due = append(due, *a)
	}
	if len(due) > 0 {
		_ = s.saveLocked()
	}
	return due
}

// RecordError 记录一次执行失败（不影响调度）。
func (s *Store) RecordError(id string, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].LastError = msg
			_ = s.saveLocked()
			return
		}
	}
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("a%d", time.Now().UnixNano())
	}
	return "a_" + hex.EncodeToString(b)
}
