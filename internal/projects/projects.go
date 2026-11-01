// Package projects 管理「小柯」的项目工作区：每个项目是磁盘上的一个目录，
// 小柯的文件读写与命令执行都被限制在所选项目目录内。
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"xiaoke/internal/tools"
)

var ErrNotFound = errors.New("project not found")

var validName = regexp.MustCompile(`^[\p{Han}\w\-]{1,32}$`)
var validID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Project 是一个项目工作区。
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"` // 绝对路径
	Template  string    `json:"template,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// TreeItem 是文件树的一项（相对项目根，斜杠分隔）。
type TreeItem struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

type indexFile struct {
	Projects []Project `json:"projects"`
}

// Manager 是项目仓库。RootFn 每次调用时取当前项目根（支持设置热更新）。
type Manager struct {
	mu        sync.Mutex
	indexPath string
	rootFn    func() string
	projects  []Project
	newID     func() string
}

// New 打开（或初始化）项目索引。
func New(indexPath string, rootFn func() string) (*Manager, error) {
	m := &Manager{indexPath: indexPath, rootFn: rootFn}
	data, err := os.ReadFile(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err == nil {
		m.projects = idx.Projects
	}
	return m, nil
}

// Root 返回当前生效的项目根目录（设置未配置时用默认值）。
func (m *Manager) Root() string {
	if r := strings.TrimSpace(m.rootFn()); r != "" {
		return r
	}
	// rootFn 的兜底由 main 注入；这里再兜一层防止空串
	return filepath.Join(".", "projects")
}

// List 返回全部项目（按创建时间倒序）。
func (m *Manager) List() []Project {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]Project(nil), m.projects...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Get 按 ID 取项目。
func (m *Manager) Get(id string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		if p.ID == id {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// Create 新建项目目录并登记。customRoot 非空时用它在那个父目录下创建（需已存在）。
func (m *Manager) Create(name, template, customRoot string, genID func() string) (Project, error) {
	name = strings.TrimSpace(name)
	if !validName.MatchString(name) {
		return Project{}, fmt.Errorf("项目名只能包含中文、字母、数字、下划线和短横线（1-32 字符）")
	}
	switch template {
	case "", "code", "article", "empty":
	default:
		return Project{}, fmt.Errorf("未知模板 %q（可选 code/article/empty）", template)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 校验与登记在同一把锁内，避免并发同名创建竞态
	for _, p := range m.projects {
		if p.Name == name {
			return Project{}, fmt.Errorf("项目已存在：%s", name)
		}
	}
	root := m.Root()
	if strings.TrimSpace(customRoot) != "" {
		abs, err := filepath.Abs(customRoot)
		if err != nil {
			return Project{}, err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return Project{}, fmt.Errorf("自定义目录 %s 不存在或不是文件夹", customRoot)
		}
		root = abs
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err == nil {
		return Project{}, fmt.Errorf("目录已存在：%s", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Project{}, err
	}
	if err := scaffold(dir, name, template); err != nil {
		return Project{}, err
	}
	p := Project{ID: genID(), Name: name, Path: dir, Template: template, CreatedAt: time.Now()}
	m.projects = append(m.projects, p)
	if err := m.save(); err != nil {
		return Project{}, err
	}
	return p, nil
}

// scaffold 按模板写入初始文件。
func scaffold(dir, name, template string) error {
	switch template {
	case "code":
		readme := fmt.Sprintf("# %s\n\n> 由小柯创建的代码项目。\n\n## 结构\n\n- `src/` 源代码\n- `docs/` 文档\n", name)
		if err := writeIfAbsent(filepath.Join(dir, "README.md"), readme); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	case "article":
		readme := fmt.Sprintf("# %s\n\n> 由小柯创建的写作项目。\n\n## 大纲\n\n1. \n2. \n3. \n\n## 正文\n\n", name)
		return writeIfAbsent(filepath.Join(dir, "README.md"), readme)
	default:
		return nil
	}
}

func writeIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// Delete 删除项目目录并从索引移除。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	var p Project
	for i, it := range m.projects {
		if it.ID == id {
			idx, p = i, it
			break
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	if err := os.RemoveAll(p.Path); err != nil {
		return err
	}
	m.projects = append(m.projects[:idx], m.projects[idx+1:]...)
	return m.save()
}

// Tree 返回项目文件树（深度≤4，≤500 项，忽略常见依赖目录）。
func (m *Manager) Tree(id string) ([]TreeItem, error) {
	p, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	// 索引可能残留已删除的目录：明确报错，避免返回空树让人误以为项目是空的
	if info, err := os.Stat(p.Path); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("项目目录不存在：%s", p.Path)
	}
	items := []TreeItem{}
	root := p.Path
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if tools.SkipDirs[name] {
				return fs.SkipDir
			}
			if strings.Count(rel, string(filepath.Separator)) >= 4 {
				return fs.SkipDir
			}
		}
		if len(items) >= 500 {
			return fs.SkipAll
		}
		items = append(items, TreeItem{Path: filepath.ToSlash(rel), IsDir: d.IsDir()})
		return nil
	})
	// 目录优先、各自字母序：文件树层次一目了然
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Path < items[j].Path
	})
	return items, nil
}

func (m *Manager) save() error {
	data, err := json.MarshalIndent(indexFile{Projects: m.projects}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.indexPath), 0o755); err != nil {
		return err
	}
	tmp := m.indexPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.indexPath)
}
