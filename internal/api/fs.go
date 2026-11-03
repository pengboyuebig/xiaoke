// 目录浏览接口：给「新建项目」的文件夹选择器用。
// 仅返回目录（不列文件），供用户在任意位置挑选项目父目录。
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// fsDirsLimit 单层返回的目录条数上限（防止误点超大目录把响应撑爆）。
const fsDirsLimit = 500

type fsDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type fsDirsResp struct {
	Path      string       `json:"path"`      // 当前目录的绝对路径
	Name      string       `json:"name"`      // 当前目录名（盘符根显示为 "C:\\"）
	Parent    string       `json:"parent"`    // 上级目录绝对路径（空 = 已到顶）
	Dirs      []fsDirEntry `json:"dirs"`      // 当前目录下的子目录
	Truncated bool         `json:"truncated"` // 是否因上限被截断
	Roots     []string     `json:"roots"`     // 可快速跳转的根（Windows 盘符；其它平台为空）
}

// listDirs 列出某个目录下的子目录。path 为空时从默认项目根目录开始。
func (h *Handler) listDirs(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))

	var dir string
	if raw == "" {
		// 起点：默认项目根目录（相对路径按进程 cwd 解析为绝对路径）
		root := h.projects.Root()
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		dir = abs
	} else {
		abs, err := filepath.Abs(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法：" + err.Error()})
			return
		}
		dir = abs
	}

	info, err := os.Stat(dir)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "目录不存在：" + dir})
		return
	}
	if !info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不是文件夹：" + dir})
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "无法读取该目录（可能没有权限）：" + err.Error()})
		return
	}

	dirs := make([]fsDirEntry, 0, len(entries))
	truncated := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if len(dirs) >= fsDirsLimit {
			truncated = true
			break
		}
		dirs = append(dirs, fsDirEntry{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
		})
	}
	// 大小写不敏感排序（Windows 习惯）
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	resp := fsDirsResp{
		Path:      dir,
		Name:      baseName(dir),
		Parent:    parentDir(dir),
		Dirs:      dirs,
		Truncated: truncated,
		Roots:     volumeRoots(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// isVolumeRoot 判断是否为盘符根（如 "D:\"、"D:"）。
// filepath.VolumeName("D:\\") 返回 "D:"，剩余部分是分隔符而非空串，
// 所以不能只判断剩余是否为空。
func isVolumeRoot(dir string) bool {
	vol := filepath.VolumeName(dir)
	if vol == "" {
		return false
	}
	rest := strings.TrimSpace(dir[len(vol):])
	return rest == "" || rest == `\` || rest == "/"
}

// baseName 返回目录显示名；盘符根（如 C:\）返回 "C:\"。
func baseName(dir string) string {
	if runtime.GOOS == "windows" && isVolumeRoot(dir) {
		return filepath.VolumeName(dir) + `\`
	}
	return filepath.Base(dir)
}

// parentDir 返回上级目录；已到顶（盘符根或根目录）时返回空串。
func parentDir(dir string) string {
	if runtime.GOOS == "windows" && isVolumeRoot(dir) {
		return ""
	}
	parent := filepath.Dir(dir)
	if parent == dir || parent == "." {
		return ""
	}
	return parent
}

// volumeRoots 返回可快速跳转的根目录（Windows 下列出存在的盘符）。
func volumeRoots() []string {
	if runtime.GOOS != "windows" {
		return []string{}
	}
	roots := make([]string, 0, 8)
	for c := 'A'; c <= 'Z'; c++ {
		vol := string(c) + `:\`
		if info, err := os.Stat(vol); err == nil && info.IsDir() {
			roots = append(roots, vol)
		}
	}
	return roots
}

// ---------- 按文件夹名查找（拖拽文件夹用） ----------
//
// 浏览器出于安全限制不会暴露拖入文件夹的绝对路径，只能拿到名字
// （DataTransferItem.webkitGetAsEntry().name）。所以拖拽后由后端在
// 有限范围内查找同名目录，再把候选路径回给前端选择。

const (
	findDirMaxVisited  = 50000                   // 最多读取多少个目录
	findDirMaxResults  = 20                      // 最多返回多少个候选
	findDirQuickBudget = 1500 * time.Millisecond // 项目根/主目录的搜索预算
	findDirDeepBudget  = 15 * time.Second        // 用户主动触发全盘搜索的预算
	findDirWorkers     = 8                       // 并发扫描的 worker 数
)

// findDirSkipDirs 查找时跳过的依赖/缓存目录（与 tools.SkipDirs 同一套约定）。
// 不跳过 build/dist：用户完全可能把项目命名成这两个名字，跳过会导致拖拽永远找不到。
var findDirSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true,
	"__pycache__": true, ".venv": true, "venv": true, ".idea": true,
}

type findDirResp struct {
	Query     string       `json:"query"`
	Dirs      []fsDirEntry `json:"dirs"`
	Truncated bool         `json:"truncated"`
}

// findDir 按名字在磁盘上查找同名目录（大小写不敏感）。
//
// 浏览器出于安全限制不暴露拖入文件夹的绝对路径，只能拿到名字
// （DataTransferItem.webkitGetAsEntry().name），所以拖拽后由后端按名查找。
//
// 搜索顺序按命中率从高到低：项目根 → 用户主目录 → 各盘符根（并发 BFS）。
// 全盘扫描用多 worker 并行，避免顺序递归在深目录结构下超时。
func (h *Handler) findDir(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("name"))
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "需要 name 参数"})
		return
	}
	// 只取纯名字：防止调用方传路径进来，也避免匹配到 ".." 之类的特殊段
	want := strings.ToLower(filepath.Base(raw))
	if want == "." || want == ".." || want == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件夹名不合法"})
		return
	}

	results := make([]fsDirEntry, 0, findDirMaxResults)
	var mu sync.Mutex
	visited := 0

	// scan 在 roots 下按层（BFS）并发搜索，depth 为最大层数。
	// 每层并发读取目录，层与层之间串行——避免 channel 队列的死锁，
	// 同时保留并行带来的速度优势。返回 true 表示因预算/上限提前结束。
	scan := func(roots []string, depth int, budget time.Duration) bool {
		deadline := time.Now().Add(budget)
		level := roots
		for d := depth; d > 0 && len(level) > 0; d-- {
			if time.Now().After(deadline) {
				return true
			}
			// 并发读这一层所有目录，收集子目录 + 命中结果
			type levelOut struct {
				dirs []string
				hits []fsDirEntry
			}
			outs := make([]levelOut, len(level))
			var wg sync.WaitGroup
			sem := make(chan struct{}, findDirWorkers)
			for i, dir := range level {
				wg.Add(1)
				go func(i int, dir string) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					if time.Now().After(deadline) {
						return
					}
					entries, err := os.ReadDir(dir)
					if err != nil {
						return // 无权限/不存在：跳过
					}
					mu.Lock()
					visited++
					mu.Unlock()
					for _, e := range entries {
						if !e.IsDir() || findDirSkipDirs[e.Name()] {
							continue
						}
						p := filepath.Join(dir, e.Name())
						if strings.EqualFold(e.Name(), want) {
							outs[i].hits = append(outs[i].hits, fsDirEntry{Name: e.Name(), Path: p})
						}
						outs[i].dirs = append(outs[i].dirs, p)
					}
				}(i, dir)
			}
			wg.Wait()

			next := make([]string, 0, len(level)*4)
			mu.Lock()
			for i := range outs {
				results = append(results, outs[i].hits...)
				next = append(next, outs[i].dirs...)
			}
			enough := len(results) >= findDirMaxResults
			mu.Unlock()
			if enough {
				return false // 结果够了，正常结束
			}
			// 下一层待扫目录超上限就截断（标记可能不全）
			if len(next) > findDirMaxVisited {
				next = next[:findDirMaxVisited]
			}
			level = next
		}
		return time.Now().After(deadline)
	}

	// 1) 项目根（新建项目的父目录通常就在这里）——通常毫秒级
	if proot := h.projects.Root(); proot != "" {
		if abs, err := filepath.Abs(proot); err == nil {
			scan([]string{abs}, 4, findDirQuickBudget)
		}
	}
	// 2) 用户主目录
	if len(results) == 0 {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			scan([]string{home}, 3, findDirQuickBudget)
		}
	}
	// 3) 兜底扫盘：仅在显式要求 deep=1 时执行。
	// 全盘搜索耗时且可能扫不全，由用户在界面上主动触发，避免每次拖拽都卡住。
	truncated := false
	if len(results) == 0 && r.URL.Query().Get("deep") == "1" {
		roots := volumeRoots()
		// 当前工作盘优先：用户的项目通常就在这台机器的常用盘上
		if cwd, err := os.Getwd(); err == nil {
			if vol := filepath.VolumeName(cwd); vol != "" {
				pref := vol + `\`
				for i, rr := range roots {
					if strings.EqualFold(rr, pref) {
						roots[0], roots[i] = roots[i], roots[0]
						break
					}
				}
			}
		}
		if len(roots) > 0 {
			truncated = scan(roots, 5, findDirDeepBudget)
		}
	}

	writeJSON(w, http.StatusOK, findDirResp{Query: want, Dirs: results, Truncated: truncated})
}
