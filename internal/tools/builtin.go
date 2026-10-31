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
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

var toolEditFile = Tool{
	Name:        "edit_file",
	Description: "对工作区内已有文件做精确替换编辑：用 old_string 定位（必须唯一匹配）并替换为 new_string。修改文件优先用它，只传改动片段，不要用 write_file 重写整个文件。多处修改可传 edits 数组一次完成（任一处失败则整批不落盘）。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件相对路径（相对项目根）"},
    "old_string": {"type": "string", "description": "要被替换的原文片段，必须与文件内容逐字符一致（含缩进）"},
    "new_string": {"type": "string", "description": "替换后的新片段；删除内容时传空串"},
    "replace_all": {"type": "boolean", "description": "true 时替换全部匹配，默认 false（仅允许唯一匹配）"},
    "edits": {
      "type": "array",
      "description": "批量编辑（与单编辑二选一）：全部校验通过才一次性写入，任一处失败整批拒绝",
      "items": {
        "type": "object",
        "properties": {
          "old_string": {"type": "string"},
          "new_string": {"type": "string"},
          "replace_all": {"type": "boolean"}
        },
        "required": ["old_string", "new_string"]
      }
    }
  },
  "required": ["path"]
}`),
	Execute: execEditFile,
}

var toolWriteFile = Tool{
	Name:        "write_file",
	Description: "把文本写入工作区内的文件（自动创建父目录，UTF-8，覆盖式写入，上限 256KB）。新建文件用它；修改已有文件优先用 edit_file。",
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
	Description: "在工作区内做全文搜索，返回「文件:行号: 内容」列表（≤100 条，按文件与行号排序）。默认大小写不敏感的子串匹配；regex=true 时按正则匹配（Go 语法，大小写敏感）。忽略 node_modules/.git 等目录和大于 1MB 的文件。",
	Name:        "search_files",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索关键词或正则表达式"},
    "path": {"type": "string", "description": "搜索起点，默认工作区根"},
    "regex": {"type": "boolean", "description": "true = query 按正则解析（默认 false）"}
  },
  "required": ["query"]
}`),
	Execute: execSearchFiles,
}

var toolFindFiles = Tool{
	Description: "按名字模糊找文件（适合记不清确切名字/拼写有出入的场景）：对工作区内相对路径做子序列评分排序，返回最像的 top 20。知道确切路径用 list_files；找文件内容用 search_files。",
	Name:        "find_files",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "文件名片段（子序列匹配，如 usrvc 能找到 user_service.go）"},
    "path": {"type": "string", "description": "搜索起点，默认工作区根"}
  },
  "required": ["query"]
}`),
	Execute: execFindFiles,
}

var toolMemoryAppend = Tool{
	Name:        "memory_append",
	Description: "把值得跨会话长期记住的信息写入记忆（用户偏好、项目结构、重要决定、被纠正过的错误）。一行一个结论，不要复述对话原文；已有内容不要重复追加。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "content": {"type": "string", "description": "要记住的内容（一句话结论）"},
    "scope": {"type": "string", "enum": ["global", "project"], "description": "global=跟随用户（跨项目），project=只属于当前项目（默认）"}
  },
  "required": ["content"]
}`),
	Execute: execMemoryAppend,
}

var toolMemoryRead = Tool{
	Name:        "memory_read",
	Description: "读取长期记忆的完整内容（系统提示词里只有截断版）。scope 同 memory_append。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "scope": {"type": "string", "enum": ["global", "project"], "description": "默认 project"}
  }
}`),
	Execute: execMemoryRead,
}

var toolTodoWrite = Tool{
	Name:        "todo_write",
	Description: "维护当前任务的待办清单（整表替换）。3 步以上的任务先建待办再动手；每完成一项立即更新状态；同一时刻最多一项 in_progress。待办会展示给用户并跨压缩保留。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "description": "完整待办列表（每次全量覆盖）",
      "items": {
        "type": "object",
        "properties": {
          "content": {"type": "string", "description": "待办内容（祈使句，如「修复登录超时」）"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"], "description": "状态"}
        },
        "required": ["content", "status"]
      }
    }
  },
  "required": ["todos"]
}`),
	Execute: execTodoWrite,
}

var toolTask = Tool{
	Name:        "task",
	Description: "派一个子代理独立完成子任务并把结果汇报给你。适用：大范围代码调研、多文件分析、可以拆出去的独立子任务。\nAgent types:\n- explore: 只读调研（list/read/search/memory_read），适合找实现、摸清结构、汇总现状；多个 explore 可并行派出（最多 2 个同时跑）\n- general-purpose: 完整工具（含写文件与命令），适合需要动手的执行类任务；同一时刻只跑一个，且与其它 general-purpose 互斥\n用法注意：prompt 必须自包含（子代理看不到本会话历史，把背景、目标、期望的汇报格式写全）。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt": {"type": "string", "description": "给子代理的完整任务说明（自包含：背景 + 目标 + 汇报要求）"},
    "subagent_type": {"type": "string", "enum": ["general-purpose", "explore"], "description": "子代理类型，默认 general-purpose"},
    "description": {"type": "string", "description": "一句话说明这个子任务是什么（给自己和用户看）"}
  },
  "required": ["prompt"]
}`),
	Execute: execTask,
}

var toolSkillRead = Tool{
	Name:        "skill_read",
	Description: "读取一个已启用技能的完整方法论（系统提示词里只有技能清单）。当清单里有技能与当前任务相关时，先读它的正文再动手；与任务无关的技能不要读。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "技能名，需与清单里的名字精确一致"}
  },
  "required": ["name"]
}`),
	Execute: execSkillRead,
}

var toolWebFetch = Tool{
	Name:        "web_fetch",
	Description: "抓取公网网页并转为纯文本（HTML 自动剥标签，JSON/纯文本原样返回；30 秒超时，正文上限 16KB）。跨主机重定向不会自动跟随，会返回目标地址让你显式确认。仅支持 http/https 公网地址；本地地址或内网服务不可用。适合：查文档、读在线文章、验证 API 返回。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "要抓取的 http/https 地址"}
  },
  "required": ["url"]
}`),
	Execute: execWebFetch,
}

var toolAskUser = Tool{
	Name:        "ask_user",
	Description: "向用户提问并等待回答。仅用于「卡在真正需要用户拍板的选择」上：方案二选一、需求不明确的关键分歧。不要用它问「我可以继续吗」这类自己能决定的事。给 2-4 个选项时用户可单选/多选，也可以自由输入；不给选项就是开放式提问。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "完整、具体的问题（用户看不到对话历史之外任何上下文）"},
    "options": {
      "type": "array",
      "items": {"type": "string"},
      "description": "候选选项（2-4 个，可省略 = 开放式提问）；推荐项放第一个并在末尾注明（推荐）"
    },
    "multi_select": {"type": "boolean", "description": "true = 允许多选（默认单选）"}
  },
  "required": ["question"]
}`),
	Execute: execAskUser,
}

var toolCronCreate = Tool{
	Name:        "cron_create",
	Description: "创建定时任务：按 cron 表达式（用户本地时区）或一次性延迟，定期无人值守执行一段 prompt（每次运行会新建一个会话）。标准 5 段表达式（分 时 日 月 周），支持 */n、列表、区间；不支持秒级与 @宏。相对延迟（delay_minutes）绝不能换算成绝对 cron 时间。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "任务名（展示给用户，保留用户的自然语言时间表述，如「每天早上9点」「每20分钟」）"},
    "cron": {"type": "string", "description": "标准 5 段 cron 表达式（本地时区）；与 delay_minutes 二选一"},
    "delay_minutes": {"type": "integer", "description": "从现在起的延迟分钟数（一次性任务）；与 cron 二选一"},
    "prompt": {"type": "string", "description": "每次触发要执行的完整指令（自包含：无人值守时用户不在线，必须写明目标与产出）"},
    "project_id": {"type": "string", "description": "绑定项目（可选）"},
    "max_runs": {"type": "integer", "description": "最多执行次数（0 或省略 = 不限）"}
  },
  "required": ["title", "prompt"]
}`),
	Execute: execCronCreate,
}

var toolCronList = Tool{
	Name:        "cron_list",
	Description: "列出全部定时任务（含下次运行时间、已运行次数、启停状态）。",
	Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	Execute:     execCronList,
}

var toolCronUpdate = Tool{
	Name:        "cron_update",
	Description: "修改定时任务：标题/表达式/prompt/次数上限/启停。省略的字段保持不变；重新启用会重算下次运行时间。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "任务 ID（cron_list 可查）"},
    "title": {"type": "string"},
    "cron": {"type": "string"},
    "prompt": {"type": "string"},
    "max_runs": {"type": "integer", "description": "-1 = 保持不变"},
    "enabled": {"type": "boolean"}
  },
  "required": ["id"]
}`),
	Execute: execCronUpdate,
}

var toolCronDelete = Tool{
	Name:        "cron_delete",
	Description: "删除一个定时任务（同时释放名额）。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "任务 ID（cron_list 可查）"}
  },
  "required": ["id"]
}`),
	Execute: execCronDelete,
}

var toolRunCommand = Tool{
	Description: "在工作区内执行白名单命令（git/npm/npx/node/python/pip）。argv 直传不经 shell，30 秒超时，输出截断 8KB。例：command=\"git\", args=[\"status\"]。is_background=true 时立即返回 task_id（适合构建/安装等长命令），用 task_output 读输出、task_kill 终止。",
	Name:        "run_command",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "命令名，如 git、npm、python"},
    "args": {"type": "array", "items": {"type": "string"}, "description": "参数列表"},
    "cwd": {"type": "string", "description": "工作目录（相对项目根，默认项目根）"},
    "is_background": {"type": "boolean", "description": "true = 后台运行，立即返回 task_id（默认 false）"}
  },
  "required": ["command"]
}`),
	Execute: execRunCommand,
}

var toolTaskOutput = Tool{
	Name:        "task_output",
	Description: "读取后台命令任务的输出与状态（running/completed/failed）。wait_ms>0 时最多等这么久让它完成（上限 30000）。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "run_command is_background 返回的任务 ID"},
    "wait_ms": {"type": "integer", "description": "等待完成的时长（毫秒，默认 0 = 不等待）"}
  },
  "required": ["task_id"]
}`),
	Execute: execTaskOutput,
}

var toolTaskKill = Tool{
	Name:        "task_kill",
	Description: "终止一个还在运行的后台命令任务。已结束的任务无需终止。",
	Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "要终止的任务 ID"}
  },
  "required": ["task_id"]
}`),
	Execute: execTaskKill,
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
			// 目录条目也计入上限，避免大仓库把内存撑爆
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
				// 与 search_files/项目树同一套跳过规则：递归浏览不应走进依赖目录
				if SkipDirs[d.Name()] {
					return fs.SkipDir
				}
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
	// 目录在前、文件在后，各自字母序：模型读目录结构时层次更清晰
	sort.Slice(entries, func(i, j int) bool {
		di, dj := strings.HasSuffix(entries[i], "/"), strings.HasSuffix(entries[j], "/")
		if di != dj {
			return di
		}
		return entries[i] < entries[j]
	})
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
	if info, statErr := os.Stat(abs); statErr == nil && env.Files != nil {
		// 同视图且文件未变：不重读，返回存根（省 token，内容已在上文）
		if env.Files.Observe(abs, info, in.Offset, in.Limit) {
			return "(文件自上次读取后没有变化，完整内容仍在你已有的上下文里，无需重读；要基于它编辑请直接用 edit_file)", nil
		}
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在：%s%s", in.Path, similarFileHint(env, abs))
		}
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

// similarFileHint 文件不存在时给出工作区内相近文件名建议（Levenshtein≤3），
// 把「打错文件名→报错→重猜」的三轮往返压成一轮。
func similarFileHint(env *Env, abs string) string {
	if env == nil || strings.TrimSpace(env.Workspace) == "" {
		return ""
	}
	target := strings.ToLower(filepath.Base(abs))
	type cand struct {
		rel   string
		score int
	}
	var best []cand
	count := 0
	_ = filepath.WalkDir(env.Workspace, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if err == nil && d.IsDir() && SkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if count >= 800 {
			return fs.SkipAll
		}
		count++
		rel, rerr := filepath.Rel(env.Workspace, p)
		if rerr != nil {
			return nil
		}
		score := levenshtein(strings.ToLower(d.Name()), target)
		if score <= 3 {
			best = append(best, cand{filepath.ToSlash(rel), score})
		}
		return nil
	})
	if len(best) == 0 {
		return ""
	}
	sort.Slice(best, func(i, j int) bool { return best[i].score < best[j].score })
	if len(best) > 3 {
		best = best[:3]
	}
	names := make([]string, 0, len(best))
	for _, c := range best {
		names = append(names, c.rel)
	}
	return "；你是想找这些文件吗：" + strings.Join(names, "、")
}

// levenshtein 编辑距离（两行 DP，O(len*len) 内存压缩到单行）。
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
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
	// 覆盖已有文件 = 修改操作：同样要求先读过（新建文件豁免）
	existed := false
	oldContent := ""
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		existed = true
		if err := env.Files.CheckEditable(abs); err != nil {
			return "", fmt.Errorf("目标文件已存在，%w", err)
		}
		oldContent = readForDiff(abs)
	}
	if err := writeAtomic(abs, in.Content); err != nil {
		return "", err
	}
	env.Files.MarkEdited(abs)
	if oldContent != "" {
		env.emitDiff(UnifiedDiff(oldContent, in.Content, 3))
	}
	if env.OnFileChange != nil {
		env.OnFileChange(abs, oldContent, existed)
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", filepath.ToSlash(in.Path), len(in.Content)), nil
}

// readForDiff 读文件内容供 diff 预览；超过 1MB 返回空串（预览不值得读入超大文件）。
func readForDiff(abs string) string {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return ""
	}
	return string(data)
}

// writeAtomic 原子写：先写同目录临时文件再 rename。直接 WriteFile 时进程被杀/断电
// 会留下半截文件——源码文件半截比写入失败糟糕得多。write_file 与 edit_file 共用。
func writeAtomic(abs, content string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".xk-tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// execEditFile 精确替换编辑：old_string 必须与文件内容唯一匹配（或 replace_all）。
// 支持批量（edits 数组）：全部编辑对同一份 pre-edit 快照校验、一次写入——
// 任一处失败整批拒绝，杜绝「改了一半」的中间态（对标批量编辑的原子性）。
//
// 匹配策略：CRLF→LF 归一化后做精确匹配（不做模糊匹配——模糊容错会把「相似但不同」
// 的两处代码混为一处）；写回时按原文件行尾风格还原。
func execEditFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Edits      []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", fmt.Errorf("需要 path 参数")
	}
	// 单编辑与批量二选一：都传拒绝（模型容易拼错格式，宁可报错不猜）
	if len(in.Edits) > 0 && (in.OldString != "" || in.NewString != "") {
		return "", fmt.Errorf("old_string/new_string 与 edits 不能同时传：单处修改用前者，多处修改用后者")
	}
	type editOp struct {
		old, new   string
		replaceAll bool
	}
	ops := make([]editOp, 0, len(in.Edits)+1)
	if len(in.Edits) > 0 {
		if len(in.Edits) > 20 {
			return "", fmt.Errorf("edits 过多（%d 处，上限 20）：拆成多次调用，每次改一个函数/区块", len(in.Edits))
		}
		for i, e := range in.Edits {
			if e.OldString == "" {
				return "", fmt.Errorf("edits[%d].old_string 不能为空", i)
			}
			if e.OldString == e.NewString {
				return "", fmt.Errorf("edits[%d] 的 old_string 与 new_string 相同", i)
			}
			ops = append(ops, editOp{old: e.OldString, new: e.NewString, replaceAll: e.ReplaceAll})
		}
	} else {
		if in.OldString == "" {
			return "", fmt.Errorf("需要 old_string（或用 edits 批量编辑）；新建文件请用 write_file")
		}
		if in.OldString == in.NewString {
			return "", fmt.Errorf("old_string 与 new_string 相同，没有需要替换的内容")
		}
		ops = append(ops, editOp{old: in.OldString, new: in.NewString, replaceAll: in.ReplaceAll})
	}
	for _, op := range ops {
		if len(op.new) > maxWriteBytes {
			return "", fmt.Errorf("new_string 超过 %dKB 上限", maxWriteBytes/1024)
		}
	}
	// 编辑走写路径校验：只读授权目录里的文件不可修改
	abs, err := env.resolveWrite(in.Path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在：%s（新建文件请用 write_file）", in.Path)
		}
		return "", err
	}
	// 新鲜度护栏：必须先读过全文、且读取后未被外部改动
	if err := env.Files.CheckEditable(abs); err != nil {
		return "", err
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%s 是二进制文件，无法按文本编辑", in.Path)
	}
	crlf := bytes.Contains(raw, []byte("\r\n"))
	norm := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	content := norm(string(raw))

	// 先对 pre-edit 快照逐条校验（数量与唯一性），任何一条不过 → 整批拒绝、
	// 不产生任何写入；通过后再按顺序应用（后续编辑作用在前面编辑的结果上，
	// 与模型从上到下写编辑列表的心智一致）。
	totalReplaced := 0
	for i, op := range ops {
		oldS, newS := norm(op.old), norm(op.new)
		count := strings.Count(content, oldS)
		label := "old_string"
		if len(ops) > 1 {
			label = fmt.Sprintf("edits[%d]", i)
		}
		switch {
		case count == 0:
			return "", fmt.Errorf("%s 在 %s 中没有找到匹配%s", label, in.Path, editNearestHint(content, op.old))
		case count > 1 && !op.replaceAll:
			return "", fmt.Errorf("%s 在 %s 中匹配到 %d 处（需要唯一匹配）：包含更多上下文使其唯一，或确认要全部替换时传 replace_all=true", label, in.Path, count)
		}
		// 校验通过才能应用；应用后 content 变化，后续编辑基于新内容校验
		//（这里把校验+应用合并在同一循环里：失败时直接 return，文件未被写过，
		// 天然保持「整批不落盘」的原子语义）
		if op.replaceAll {
			totalReplaced += count
			content = strings.ReplaceAll(content, oldS, newS)
		} else {
			totalReplaced++
			content = strings.Replace(content, oldS, newS, 1)
		}
	}
	if crlf {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	if err := writeAtomic(abs, content); err != nil {
		return "", err
	}
	env.Files.MarkEdited(abs)
	env.emitDiff(UnifiedDiff(string(raw), content, 3))
	if env.OnFileChange != nil {
		env.OnFileChange(abs, string(raw), true)
	}
	batchNote := ""
	if len(ops) > 1 {
		batchNote = fmt.Sprintf("（批量 %d 处）", len(ops))
	}
	return fmt.Sprintf("已修改 %s%s（替换 %d 处）", filepath.ToSlash(in.Path), batchNote, totalReplaced), nil
}

// editNearestHint 在 0 匹配时给出「最近似位置」提示，把「失败→重读→重试」的
// 三轮往返压缩成一轮：先试 old_string 首行原文，再退化为首行最长 token。
func editNearestHint(content, oldS string) string {
	first := strings.TrimSpace(strings.SplitN(oldS, "\n", 2)[0])
	if first == "" {
		return "（请重新 read_file 核对内容后再试）"
	}
	if idx := strings.Index(content, first); idx >= 0 {
		line := 1 + strings.Count(content[:idx], "\n")
		return fmt.Sprintf("；它的第一行在第 %d 行附近出现过，但整段不一致（后续行可能有差异）——重新 read_file 该区域核对后再试", line)
	}
	// 首行整行没有：取首行最长 token 再找一次
	token := ""
	for _, w := range strings.FieldsFunc(first, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '(' || r == ')' || r == '{' || r == '}' || r == ',' || r == ';'
	}) {
		if len(w) > len(token) {
			token = w
		}
	}
	if token != "" {
		if idx := strings.Index(content, token); idx >= 0 {
			line := 1 + strings.Count(content[:idx], "\n")
			trimmed := strings.TrimSpace(strings.SplitN(content[idx:], "\n", 2)[0])
			trimmed = truncateRunes(trimmed, 120)
			return fmt.Sprintf("；内容最接近的是第 %d 行：%s——请重新 read_file 核对后再试", line, trimmed)
		}
	}
	return "（请重新 read_file 核对内容后再试）"
}

// SkipDirs 是递归遍历（列目录/搜索）统一跳过的依赖/构建目录。
var SkipDirs = map[string]bool{
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

// TruncateMiddle 中间截断：保留头尾砍中缝（导出供 chat 包复用）。
// 错误信息常在尾部、正文常在头部，一刀切尾会丢报错。UTF-8 边界安全。
func TruncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const marker = "\n…(中间输出已截断)…\n"
	head := max*2/3 - len(marker)/2
	tail := max - head - len(marker)
	if head < 1 || tail < 1 {
		return truncateBytes(s, max)
	}
	start := len(s) - tail
	// 尾部对齐到 rune 起始边界：向后收缩（不是向前扩，否则总长会超 max）
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return truncateBytes(s, head) + marker + s[start:]
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

// binaryExts 是无需读入做文本匹配的常见二进制/资源扩展名。
// 搜索跳过它们：既省 IO，也避免把图片字节误判成「无命中文件」白扫一遍。
var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".7z": true, ".rar": true,
	".mp3": true, ".mp4": true, ".mov": true, ".avi": true, ".webm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".class": true,
	".pyc": true, ".o": true, ".a": true, ".lib": true, ".wasm": true,
	".sqlite": true, ".db": true, ".bin": true, ".dat": true,
}

func execSearchFiles(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
		Path  string `json:"path"`
		Regex bool   `json:"regex"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("需要 query 参数")
	}
	base, err := env.resolveRead(in.Path)
	if err != nil {
		return "", err
	}
	// 匹配器二选一：正则（大小写敏感，Go 语法）或子串（大小写不敏感）。
	// 非法正则明确报错而不是静默降级成子串——模型写的 pattern 语法错了该重写。
	var re *regexp.Regexp
	q := strings.ToLower(in.Query)
	if in.Regex {
		re, err = regexp.Compile(in.Query)
		if err != nil {
			return "", fmt.Errorf("正则表达式不合法: %v", err)
		}
	}

	// 两阶段：先串行收集待扫描文件（WalkDir 本身是 IO 串行的），
	// 再用 worker 池并行读文件做匹配——大仓库搜索时长以 IO 为主，并行收益明显。
	type candidate struct {
		path string
		rel  string
	}
	var candidates []candidate
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if len(candidates) >= maxSearchFiles {
			return fs.SkipAll
		}
		if binaryExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > 1024*1024 {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		candidates = append(candidates, candidate{path: p, rel: filepath.ToSlash(rel)})
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	type searchHit struct {
		rel  string
		line int
		text string
	}
	var (
		mu        sync.Mutex
		hits      []searchHit
		scanned   int
		truncated bool
	)
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 2 {
		workers = 1
	}
	match := func(line string) bool {
		if re != nil {
			return re.MatchString(line)
		}
		return strings.Contains(strings.ToLower(line), q)
	}
	jobs := make(chan candidate)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				mu.Lock()
				full := len(hits) >= maxSearchHits
				mu.Unlock()
				if full {
					continue
				}
				fileHits := searchOneFile(c.path, c.rel, match, maxSearchHits)
				mu.Lock()
				scanned++
				for _, h := range fileHits {
					if len(hits) >= maxSearchHits {
						truncated = true
						break
					}
					hits = append(hits, h)
				}
				mu.Unlock()
			}
		}()
	}
	for _, c := range candidates {
		// 已够命中数就不再投喂，尽快让 worker 收工
		mu.Lock()
		full := len(hits) >= maxSearchHits
		mu.Unlock()
		if full {
			break
		}
		select {
		case jobs <- c:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()

	if len(hits) == 0 {
		hint := ""
		if re != nil {
			hint = "（正则模式）"
		}
		return fmt.Sprintf("未找到包含 %q 的内容%s（扫描 %d 个文件）", in.Query, hint, scanned), nil
	}
	// 确定性输出：并行 worker 的完成顺序不定，按「文件 + 行号」排序，
	// 模型连续两次搜索同一内容看到同样的结果，才能稳定引用。
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rel != hits[j].rel {
			return hits[i].rel < hits[j].rel
		}
		return hits[i].line < hits[j].line
	})
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s:%d: %s", h.rel, h.line, h.text))
	}
	if truncated {
		out = append(out, fmt.Sprintf("…（已达 %d 条上限）", maxSearchHits))
	}
	return strings.Join(out, "\n"), nil
}

// searchOneFile 读单个文件做匹配，返回命中行（调用方决定子串或正则）。
func searchOneFile(path, rel string, match func(string) bool, limit int) []struct {
	rel  string
	line int
	text string
} {
	raw, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(raw, 0) >= 0 {
		return nil
	}
	hits := []struct {
		rel  string
		line int
		text string
	}{}
	for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if match(line) {
			trimmed := strings.TrimSpace(line)
			if len([]rune(trimmed)) > 200 {
				trimmed = truncateRunes(trimmed, 200) + "…"
			}
			hits = append(hits, struct {
				rel  string
				line int
				text string
			}{rel, i + 1, trimmed})
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

// ---------- find_files：路径模糊搜索 ----------

// fuzzyPathScore 对相对路径做子序列匹配评分：query 的每个字符按顺序出现在
// path 中才算命中；分数偏向「连续片段」「路径段边界」「靠前位置」。
// 对标主流编辑器的模糊匹配启发式（fzf/nucleo 的简化版）。
func fuzzyPathScore(path, query string) (int, bool) {
	if query == "" {
		return 0, false
	}
	p := []rune(path)
	q := []rune(query)
	if len(q) > len(p) {
		return 0, false
	}
	score := 0
	pi := 0
	lastMatch := -2
	for _, qc := range q {
		found := -1
		for i := pi; i < len(p); i++ {
			if p[i] == qc {
				found = i
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		score += 10
		if found == lastMatch+1 {
			score += 15 // 连续匹配：query 是 path 的一段连续子串
		}
		if found == 0 || isPathBoundary(p[found-1]) {
			score += 12 // 段边界（文件名开头 / 分隔符后）：名字级命中比散落命中值钱
		}
		lastMatch = found
		pi = found + 1
	}
	// 总长惩罚：同样的命中，短路径信息密度更高
	score -= len(p) - len(q)
	return score, true
}

func isPathBoundary(r rune) bool {
	switch r {
	case '/', '\\', '_', '-', '.', ' ':
		return true
	}
	return false
}

func execFindFiles(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
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
	const (
		maxFuzzyCandidates = 2000
		maxFuzzyResults    = 20
	)
	type scored struct {
		rel   string
		score int
	}
	var matches []scored
	count := 0
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if SkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if count >= maxFuzzyCandidates {
			return fs.SkipAll
		}
		count++
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if s, ok := fuzzyPathScore(strings.ToLower(rel), strings.ToLower(in.Query)); ok {
			matches = append(matches, scored{rel, s})
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if len(matches) == 0 {
		return fmt.Sprintf("没有模糊匹配到 %q（扫描 %d 个文件）。知道部分内容时改用 search_files。", in.Query, count), nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].rel < matches[j].rel
	})
	if len(matches) > maxFuzzyResults {
		matches = matches[:maxFuzzyResults]
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, fmt.Sprintf("%s", m.rel))
	}
	return strings.Join(out, "\n"), nil
}

// ---------- 会话级工具（依赖 Env 注入的回调；回调为 nil 时明确报错） ----------

// validSubagentTypes 是子代理的合法类型（与工具 schema 的 enum 一致）。
var validSubagentTypes = map[string]bool{
	"general-purpose": true,
	"explore":         true,
}

// maxSubagentPromptBytes 子任务说明的字节上限（子代理拿不到会话历史，
// 但也不该把整本书塞进一条任务说明）。
const maxSubagentPromptBytes = 32 * 1024

func execTask(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "", fmt.Errorf("需要 prompt 参数（子代理看不到本会话历史，任务说明必须自包含）")
	}
	if len(in.Prompt) > maxSubagentPromptBytes {
		return "", fmt.Errorf("prompt 过大（%dKB，上限 %dKB）：把任务拆小，或先让子代理读项目里的文件", len(in.Prompt)/1024, maxSubagentPromptBytes/1024)
	}
	kind := in.SubagentType
	if kind == "" {
		kind = "general-purpose"
	}
	if !validSubagentTypes[kind] {
		return "", fmt.Errorf("未知子代理类型 %q（可选：general-purpose / explore）", in.SubagentType)
	}
	if env.RunSubagent == nil {
		return "", fmt.Errorf("子代理功能当前不可用")
	}
	out, err := env.RunSubagent(kind, in.Prompt)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("子代理没有返回任何内容，请换更具体的任务说明重试")
	}
	return out, nil
}

// validTodoStatus 是 todo 状态的合法取值。
var validTodoStatus = map[string]bool{
	"pending": true, "in_progress": true, "completed": true,
}

// execTodoWrite 整表替换待办：校验状态合法性与条目数，然后交给 Env 回调持久化。
func execTodoWrite(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Todos []TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if len(in.Todos) == 0 {
		return "", fmt.Errorf("todos 不能为空（全部完成时保留列表并标记 completed，方便用户核对）")
	}
	if len(in.Todos) > 50 {
		return "", fmt.Errorf("待办过多（%d 条，上限 50）：拆成阶段性条目，一项一个可验证的结果", len(in.Todos))
	}
	inProgress := 0
	for i := range in.Todos {
		in.Todos[i].Content = strings.TrimSpace(in.Todos[i].Content)
		if in.Todos[i].Content == "" {
			return "", fmt.Errorf("第 %d 条待办内容为空", i+1)
		}
		if !validTodoStatus[in.Todos[i].Status] {
			return "", fmt.Errorf("第 %d 条状态 %q 不合法（pending/in_progress/completed）", i+1, in.Todos[i].Status)
		}
		if in.Todos[i].Status == "in_progress" {
			inProgress++
		}
	}
	if inProgress > 1 {
		return "", fmt.Errorf("同一时刻只能有一项 in_progress（当前 %d 项）", inProgress)
	}
	if env.SetTodos == nil {
		return "", fmt.Errorf("待办功能当前不可用")
	}
	if err := env.SetTodos(in.Todos); err != nil {
		return "", err
	}
	return fmt.Sprintf("已更新待办（%d 项：%d 已完成 / %d 进行中 / %d 待开始）",
		len(in.Todos), countStatus(in.Todos, "completed"), inProgress, countStatus(in.Todos, "pending")), nil
}

func countStatus(todos []TodoItem, status string) int {
	n := 0
	for _, t := range todos {
		if t.Status == status {
			n++
		}
	}
	return n
}

func execMemoryAppend(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Content) == "" {
		return "", fmt.Errorf("需要 content 参数")
	}
	if env.MemoryAppend == nil {
		return "", fmt.Errorf("记忆功能当前不可用")
	}
	scope := in.Scope
	if scope == "" {
		scope = "project"
	}
	return env.MemoryAppend(scope, in.Content)
}

func execMemoryRead(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(args, &in)
	if env.MemoryRead == nil {
		return "", fmt.Errorf("记忆功能当前不可用")
	}
	scope := in.Scope
	if scope == "" {
		scope = "project"
	}
	return env.MemoryRead(scope)
}

func execSkillRead(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		return "", fmt.Errorf("需要 name 参数（技能名，与清单精确一致）")
	}
	if env.SkillRead == nil {
		return "", fmt.Errorf("技能功能当前不可用")
	}
	return env.SkillRead(in.Name)
}

// execAskUser 阻塞等待用户回答（Env.AskUser 回调）。ctx 取消（用户停止生成/
// 断开连接）时立即返回，不给回调留悬挂等待。
func execAskUser(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Question    string   `json:"question"`
		Options     []string `json:"options"`
		MultiSelect bool     `json:"multi_select"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Question) == "" {
		return "", fmt.Errorf("需要 question 参数")
	}
	if len(in.Options) > 6 {
		return "", fmt.Errorf("选项过多（%d 个，上限 6 个）：只给真正候选的方案", len(in.Options))
	}
	if env.AskUser == nil {
		return "", fmt.Errorf("当前会话无法向用户提问")
	}
	done := make(chan struct{})
	var answer, askErr string
	go func() {
		defer close(done)
		a, err := env.AskUser(in.Question, in.Options, in.MultiSelect)
		answer, askErr = a, ""
		if err != nil {
			askErr = err.Error()
		}
	}()
	select {
	case <-done:
		if askErr != "" {
			return "", fmt.Errorf("%s", askErr)
		}
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ---------- 定时任务工具（依赖 Env 回调） ----------

func execCronCreate(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Title     string `json:"title"`
		Cron      string `json:"cron"`
		DelayMin  int    `json:"delay_minutes"`
		Prompt    string `json:"prompt"`
		ProjectID string `json:"project_id"`
		MaxRuns   int    `json:"max_runs"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Prompt) == "" {
		return "", fmt.Errorf("需要 title 与 prompt 参数")
	}
	if in.Cron == "" && in.DelayMin <= 0 {
		return "", fmt.Errorf("需要 cron 表达式或 delay_minutes（二选一）")
	}
	if in.Cron != "" && in.DelayMin > 0 {
		return "", fmt.Errorf("cron 与 delay_minutes 只能二选一")
	}
	if env.CronCreate == nil {
		return "", fmt.Errorf("当前会话不允许创建定时任务（自动化触发的会话禁止再建，防递归）")
	}
	expr := in.Cron
	maxRuns := in.MaxRuns
	if in.DelayMin > 0 {
		if in.DelayMin < 1 || in.DelayMin > 525600 {
			return "", fmt.Errorf("delay_minutes 取值 1..525600")
		}
		// 一次性延迟任务：以「每分钟检查」的表达式 + maxRuns=1 表达，
		// 真实下一次触发由调度器按 anchor 语义换算（见 cron 包）
		expr = fmt.Sprintf("once:%d", in.DelayMin)
		if maxRuns <= 0 {
			maxRuns = 1
		}
	}
	return env.CronCreate(in.Title, expr, in.Prompt, in.ProjectID, maxRuns)
}

func execCronList(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	if env.CronList == nil {
		return "", fmt.Errorf("定时任务功能当前不可用")
	}
	return env.CronList()
}

func execCronUpdate(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Cron    string `json:"cron"`
		Prompt  string `json:"prompt"`
		MaxRuns int    `json:"max_runs"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.ID) == "" {
		return "", fmt.Errorf("需要 id 参数")
	}
	if env.CronUpdate == nil {
		return "", fmt.Errorf("定时任务功能当前不可用")
	}
	return env.CronUpdate(in.ID, in.Title, in.Cron, in.Prompt, in.MaxRuns, in.Enabled)
}

func execCronDelete(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return "", fmt.Errorf("需要 id 参数")
	}
	if env.CronDelete == nil {
		return "", fmt.Errorf("定时任务功能当前不可用")
	}
	return env.CronDelete(in.ID)
}

func execRunCommand(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		Command      string   `json:"command"`
		Args         []string `json:"args"`
		Cwd          string   `json:"cwd"`
		IsBackground bool     `json:"is_background"`
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
	if in.IsBackground {
		return startBackgroundCommand(in.Command, in.Args, cwd, env.OnTaskDone)
	}
	cctx, cancel := context.WithTimeout(ctx, commandTimeout(env))
	defer cancel()
	cmd := exec.CommandContext(cctx, in.Command, in.Args...)
	cmd.Dir = cwd
	// 分页器必杀：git diff/log 在交互终端会起 pager 等输入，挂满 30s 超时。
	// 子进程继承的环境里明确禁用分页，输出直落管道。
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	// 超时自动转后台（对标主流 agent 的 auto_on_timeout）：前台超时不杀掉任务
	// 判定失败，而是把同一条命令转为后台任务继续跑，返回 task_id 让模型
	// 用 task_output 追进度——长构建/长测试不再被超时腰斩。
	if runErr != nil && cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		bgID, bgErr := startBackgroundCommand(in.Command, in.Args, cwd, env.OnTaskDone)
		if bgErr == nil {
			head := fmt.Sprintf("$ %s %s\n", in.Command, strings.Join(in.Args, " "))
			timed := fmt.Sprintf("%s\n（前台等待 %s 超时，已自动转为后台继续运行：%s\n用 task_output 读取进度与结果）",
				head, commandTimeout(env), bgID)
			return timed, nil
		}
		// 转后台失败：按普通超时失败处理
	}
	out := buf.String()
	if len(out) > maxOutBytes {
		out = truncateBytes(out, maxOutBytes) + "\n…(输出已按 8KB 截断)"
	}
	if !utf8.ValidString(out) {
		// 中文 Windows 控制台程序可能输出 GBK：不乱转码（会造出错误的字），
		// 明确告知模型输出编码可疑，建议用 python 之类显式以 UTF-8 输出重跑。
		out += "\n…（注意：以上输出不是合法 UTF-8，可能是 GBK 编码的中文；据此判断时先确认编码）"
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

// ---------- 后台命令任务（对标主流 agent 的 is_background + task_output/kill） ----------

// backgroundTaskMaxDuration 是后台任务的硬超时：防「忘了杀」的任务永远挂着。
const backgroundTaskMaxDuration = 10 * time.Minute

// maxBackgroundTasks 是注册表里保留的任务数上限（含已完成）：超限清最旧的完成项。
const maxBackgroundTasks = 32

type backgroundTask struct {
	id      string
	cmdline string
	cmd     *exec.Cmd
	wait    chan struct{} // cmd.Wait 返回后关闭
	exitErr error
	bufMu   sync.Mutex
	buf     []byte // 输出累计（超 64KB 丢头部，保留尾部——错误信息常在尾部）
	started time.Time
}

func (t *backgroundTask) appendOutput(p []byte) {
	t.bufMu.Lock()
	defer t.bufMu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxBackgroundOutputBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-maxBackgroundOutputBytes:]...)
	}
}

func (t *backgroundTask) output() string {
	t.bufMu.Lock()
	defer t.bufMu.Unlock()
	return string(t.buf)
}

const maxBackgroundOutputBytes = 64 * 1024

var (
	bgMu    sync.Mutex
	bgSeq   int
	bgTasks = map[string]*backgroundTask{}
)

// startBackgroundCommand 启动后台任务。生命周期独立于 HTTP 请求：
// 用独立的超时 context（10 分钟硬顶），请求结束/断开不影响任务运行。
// onDone 在任务进程结束（含失败/被杀）时以 task_id 回调（nil = 不回调），
// 供会话层做「任务完成自动唤醒」。
func startBackgroundCommand(command string, args []string, cwd string, onDone func(taskID string)) (string, error) {
	cctx, cancel := context.WithTimeout(context.Background(), backgroundTaskMaxDuration)
	cmd := exec.CommandContext(cctx, command, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0")

	bgMu.Lock()
	// 注册表超限：先清最旧的已完成任务
	for len(bgTasks) >= maxBackgroundTasks {
		var oldest string
		var oldestT time.Time
		for id, t := range bgTasks {
			select {
			case <-t.wait: // 已结束
			default:
				continue
			}
			if oldest == "" || t.started.Before(oldestT) {
				oldest, oldestT = id, t.started
			}
		}
		if oldest == "" {
			break // 全部还在跑：不再清理，让上限被突破一次（极端场景）
		}
		delete(bgTasks, oldest)
	}
	bgSeq++
	id := fmt.Sprintf("bg_%d_%d", time.Now().UnixMilli(), bgSeq)
	t := &backgroundTask{
		id:      id,
		cmdline: strings.TrimSpace(command + " " + strings.Join(args, " ")),
		cmd:     cmd,
		wait:    make(chan struct{}),
		started: time.Now(),
	}
	bgTasks[id] = t
	bgMu.Unlock()

	// 显式建管道合并 stdout+stderr：StdoutPipe() 不会填充 cmd.Stdout 字段，
	// 「cmd.Stderr = cmd.Stdout」实际等于把 stderr 设成 nil（输出漏到服务控制台）。
	pr, pw, err := os.Pipe()
	if err != nil {
		cancel()
		return "", err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		cancel()
		return "", fmt.Errorf("启动后台命令失败: %w", err)
	}
	// 父进程侧立即关掉写端：子进程退出时 reader 才能读到 EOF
	pw.Close()
	go func() {
		defer close(t.wait)
		defer cancel()
		defer pr.Close()
		buf := make([]byte, 4096)
		for {
			n, rerr := pr.Read(buf)
			if n > 0 {
				t.appendOutput(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		t.exitErr = cmd.Wait()
		if onDone != nil {
			onDone(t.id)
		}
	}()
	return fmt.Sprintf("后台任务已启动 task_id=%s\n$ %s\n用 task_output 读取输出与状态；长命令（构建/安装）不要用前台模式干等。", id, t.cmdline), nil
}

// BackgroundTaskResult 供 chat 层实现「后台任务完成唤醒」：返回命令行、
// 输出尾部（4KB）与状态（completed/failed/running）。ok=false 表示任务不存在
//（可能已被注册表清理）。
func BackgroundTaskResult(id string) (cmdline, output, state string, ok bool) {
	bgMu.Lock()
	t, exist := bgTasks[id]
	bgMu.Unlock()
	if !exist {
		return "", "", "", false
	}
	select {
	case <-t.wait:
		state = "completed"
		if t.exitErr != nil {
			state = "failed"
		}
	default:
		return t.cmdline, "", "running", true
	}
	out := t.output()
	if len(out) > 4096 {
		out = out[len(out)-4096:]
	}
	return t.cmdline, out, state, true
}

// getBackgroundTask 查任务（不存在报错并列出当前任务）。
func getBackgroundTask(id string) (*backgroundTask, error) {	bgMu.Lock()
	defer bgMu.Unlock()
	t, ok := bgTasks[id]
	if !ok {
		var ids []string
		for tid, tk := range bgTasks {
			select {
			case <-tk.wait:
				ids = append(ids, tid+"(已完成)")
			default:
				ids = append(ids, tid+"(运行中)")
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("任务 %q 不存在；当前没有后台任务", id)
		}
		return nil, fmt.Errorf("任务 %q 不存在；当前任务：%s", id, strings.Join(ids, ", "))
	}
	return t, nil
}

func execTaskOutput(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		TaskID string `json:"task_id"`
		WaitMs int    `json:"wait_ms"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.TaskID) == "" {
		return "", fmt.Errorf("需要 task_id 参数")
	}
	t, err := getBackgroundTask(in.TaskID)
	if err != nil {
		return "", err
	}
	// 可选等待：最多 30 秒
	if in.WaitMs > 0 {
		wait := time.Duration(in.WaitMs) * time.Millisecond
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-t.wait:
		case <-time.After(wait):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	out := t.output()
	if len(out) > maxOutBytes {
		// 保头尾砍中缝：启动信息在头部、最新进度/报错在尾部，只保头会丢报错
		out = TruncateMiddle(out, maxOutBytes)
	}
	select {
	case <-t.wait:
		status := "completed"
		if t.exitErr != nil {
			status = fmt.Sprintf("failed（%v）", t.exitErr)
		}
		return fmt.Sprintf("任务 %s 已结束：%s\n$ %s\n%s", t.id, status, t.cmdline, orLine(out)), nil
	default:
		return fmt.Sprintf("任务 %s 仍在运行（已 %d 秒）\n$ %s\n%s", t.id, int(time.Since(t.started).Seconds()), t.cmdline, orLine(out)), nil
	}
}

func execTaskKill(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.TaskID) == "" {
		return "", fmt.Errorf("需要 task_id 参数")
	}
	t, err := getBackgroundTask(in.TaskID)
	if err != nil {
		return "", err
	}
	select {
	case <-t.wait:
		return fmt.Sprintf("任务 %s 已经结束，无需终止", t.id), nil
	default:
	}
	if err := t.cmd.Process.Kill(); err != nil {
		return "", fmt.Errorf("终止失败: %w", err)
	}
	<-t.wait // 等 Wait 回收，避免僵尸进程
	return fmt.Sprintf("已终止任务 %s（$ %s）", t.id, t.cmdline), nil
}

// orLine 给空输出一个占位（task_output 用）。
func orLine(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（暂无输出）"
	}
	return s
}

// commandTimeout 返回命令超时：settings 可配（秒），缺省 30s。
func commandTimeout(env *Env) time.Duration {
	if env.CommandTimeoutSec > 0 {
		return time.Duration(env.CommandTimeoutSec) * time.Second
	}
	return cmdTimeout
}
