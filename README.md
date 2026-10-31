# 小柯 · 独立网页版智能体

以「小柯」为名的个人 AI 助手：界面仿 [opensquilla](https://github.com/TokenRhythm/opensquilla) 的暗色控制台风格（石墨底 + 品牌橙），头像为灰原哀，每条最终答复以粗体「**小柯**」开头。

**能做什么：**

- 💬 流式对话（Markdown / 代码高亮 / 工具过程可视化 / 左右分区气泡）
- 🤖 **多智能体**：设置页可新建多个智能体（名字、头像 emoji 或上传图片、人格提示词、工具开关），侧栏一键切换，会话自动绑定所选智能体。内置的「小柯」（人格原型：灰原哀）对用户隐藏、不可编辑或删除，始终可用
- 📚 **技能系统**：写一条 Markdown 技能（如「金字塔写作」），启用后注入系统提示词（总量上限 16KB，超限截断并提示），随时生效
- ⚙️ **设置中心**：整页导航（智能体 / 技能 / 模型 / MCP / 目录 / 高级），模型 API 界面配置热生效、一键测试连接拉模型列表
- 🔌 **MCP 工具**：内置标准 MCP 客户端（stdio），可添加任意 MCP server；一键添加官方 filesystem 服务（默认只读过滤）看代码；每个 server 可单独配置调用超时
- 📁 **项目工作区**：新建项目（代码/文章模板），小柯在项目里读写文件、跑白名单命令，真正帮你写代码、写文章
- 🧠 **思考程度**：对话页右下角随时调节（关/极简/低/中/高），浏览器记住选择；计划模式未指定时自动用「高」
- 🗜️ **上下文压缩**：历史字节占用达到预算 **70% 水位**时才压缩（上下文充足时绝不触发），把旧回合总结成摘要（用当前模型，持久化到会话，可在设置关闭），而不是硬裁掉；被动裁剪始终兜底。压缩发生时会话内弹出提示条，透明可见
- 🏷️ **会话命名**：首条消息自动生成标题（批准计划的隐藏指令不参与）；**双击侧栏标题可重命名**，清空标题则回退自动命名
- ⚡ **并行工具**：单轮多个只读工具并发执行（上限 4），写类工具保持串行；连接池调优降低首 token 延迟
- 🔍 **会话搜索**：侧栏按标题/ID 本地过滤
- ✍️ **输入草稿**：按会话保存输入框草稿，切会话/误刷新不丢
- 🛠️ **工具加固**：递归列目录跳过 node_modules；全文搜索并行扫描并跳过二进制；write_file 原子写（临时文件+rename）；run_command 禁用分页器（git 不再挂起）、超时可配、GBK 输出明确标注
- ✏️ **精确编辑**：`edit_file` 按唯一匹配的 old/new 片段替换文件（改文件不再整篇重写），零匹配时提示最近似位置、多匹配要求加上下文或 replace_all，CRLF 行尾自动保留
- 📋 **待办清单**：`todo_write` 维护任务列表，对话流实时展示进度卡片；未完成待办在上下文压缩后仍然保留，长任务不丢进度
- 📜 **项目规则**：项目根下的 `AGENTS.md` / `CLAUDE.md` 自动发现并注入（防伪标签转义，16KB/文件上限），仓库约定跟着项目走
- 🧷 **长期记忆**：`memory_append` / `memory_read` 跨会话记住用户偏好与项目知识（全局 + 项目两级 MEMORY.md），设置 → 高级可手工整理
- 📊 **用量统计**：对话完成事件携带本次 token 用量（优先上游真实 usage，缺失时 bytes/4 估算），会话累计用量随会话持久化

- **后端**：Go 1.22+ 纯标准库（零第三方依赖），SSE + 工具调用循环 + MCP 客户端全部手写
- **前端**：Vue 3 + Vite + TypeScript，marked + DOMPurify + highlight.js
- **模型**：任意 OpenAI 兼容上游（DeepSeek / 本地 opencode 网关 / OpenCode Zen…），内置免 Key 的 mock 演示模式（可模拟一次工具调用，验证全链路）；支持配置单次回复 max_tokens 上限

## 快速开始

双击 `start.bat`（首次自动 npm install + build + go build，然后开浏览器），或手动：

```bat
cd web && npm install && npm run build && cd ..
go build -o xiaoke.exe .
xiaoke.exe
```

打开 **http://127.0.0.1:18790**。默认 mock 演示模式，无需 API Key；在项目里说「帮我写一个文件」可以看到完整的工具调用演示。

> 改了源码之后请用 `start.bat --rebuild`（或 `./start.sh --rebuild`）强制重建。默认脚本只在产物不存在时构建，直接双击可能仍在跑旧二进制。

前端热更新开发：`cd web && npm run dev`（:5175，/api、/assets、/healthz 自动代理）。类型检查：`npm run typecheck`。

## 三步接上真实模型（设置中心）

1. 点侧边栏右上角 **⚙ 设置**
2. 「模型 API」里选预设（DeepSeek 官方 / 本地 opencode 网关 / OpenCode Zen）或手填 Base URL，粘贴 API Key，点 **测试连接**（会拉取模型列表供下拉选择）
3. **保存并应用**——热生效，侧栏底部指示灯由黄变绿

常用上游：

| 上游 | Base URL | 说明 |
|---|---|---|
| DeepSeek 官方 | `https://api.deepseek.com` | 模型如 `deepseek-chat`（支持 function calling） |
| 本地 opencode 网关 | `http://localhost:3000/api/v1` | Key 形如 `oc_…`（`npm run create-user` 生成），`GET /api/v1/models` 可拉列表 |
| OpenCode Zen | `https://opencode.ai/zen/v1` | 免费模型带 `-free` 后缀 |

环境变量（优先级最高，凭据可不落盘）：`XIAOKE_API_KEY`、`XIAOKE_BASE_URL`、`XIAOKE_MODEL`、`XIAOKE_MODE`、`XIAOKE_ADDR`。

## 让小柯看到你的代码（MCP + 只读目录）

设置 → **MCP** 分区：

- 在「一键添加 filesystem 服务」里填绝对路径，点 **＋ 添加** —— 会自动生成一条 npx 版官方 filesystem server 配置，并**默认勾选「只读」**
- 每条服务的参数**每行一个**，路径里的空格会原样保留（不再用空格切分）；也可以直接改卡片里的参数框
- 「只读」勾选后，`write/edit/create/move/delete/...` 这类工具不会出现在模型可用工具里；取消勾选才会把写能力暴露出去
- 也可以「手动添加其它 MCP 服务」（name / command / 参数，参数每行一个），与 opensquilla 的 `[[mcp.servers]]` 形态一致
- 保存后服务作为常驻进程启动；「刷新并重试」会重启已退出/启动失败的 server，正在运行的不动

之后接上真实模型，就可以直接问「看看这个项目的路由是怎么写的」这类问题，小柯会自己调 MCP 工具去读代码。

> 说明：「只读」是在**工具暴露层**过滤写类工具，不是操作系统权限隔离。第三方 MCP server 仍以你的用户权限运行；只安装可信的 server，别把系统根目录授权给它。

## 项目工作区（写代码 / 写文章）

- 侧边栏「项目」区 **＋ 新建项目**：起名字、选模板（空项目 / 代码 `src/+docs/` / 文章 `大纲+正文`），默认创建在小柯目录的 `projects\` 下，也可以填自定义父目录
- 项目目录若被外部删除，下次对话会自动重建（索引保留，不会出现"写不进去又没报错"）
- 点项目进入项目模式（顶部有横幅），此会话绑定的工具环境：
  - **可读写**：项目目录（`write_file` / `read_file` / `list_files` / `search_files`）
  - **可执行**：白名单命令（默认 git/npm/npx/node/python/pip，argv 直传不经 shell，30 秒超时，输出截断 8KB）
  - **只读**：设置里授权的查看目录
- 直接说需求即可，例如「在项目里写一个 Python 脚本统计行数，然后跑一遍」——小柯会自己 list → write → run → 汇报结果
- **工具调用记录自动整理**：同一轮的多次调用合并成一个折叠块，收起时一行一条摘要
  （`list_files . → README.md`，结果绿色标成功、红色标失败），点「展开 ▼」看完整参数与结果，默认自动收起
- **长代码自动折叠**：回答里超过 10 行的代码块默认只显示一行摘要（`html · 43 行（点击展开）`），
  步骤标题与短命令照常可见，点摘要展开完整代码（带语法高亮）

## 计划模式 ⇄ 开发模式（先调研，再批准，后开工）

输入框左侧的胶囊按钮切换工作模式，选择记在浏览器本地：

| 模式 | 能力 | 行为 |
|---|---|---|
| 📋 **计划模式** | 只读（`list_files` / `read_file` / `search_files` + 只读 MCP），禁写文件、禁执行命令 | 强制先调研再输出结构化计划：目标 / 现状调研 / 实施步骤（编号）/ 风险与边界 |
| 🛠️ **开发模式** | 完整能力（读写文件 + 白名单命令） | 按已确认的计划分步实施，每步汇报 |

**批准流程**：计划模式完成一轮后，回合末尾出现批准条，三个选择
- 「**✅ 批准并开始开发**」→ 自动切到开发模式并发出「开始」（该指令不会作为消息显示在对话里）→ 立即按计划开工
- 「**✏️ 我来说怎么做**」→ 展开输入框，写下你希望的实现方式（如「不要用 SVG，改用 Canvas」），点「按我的方式开工」→ 切开发模式并按你的要求执行（你的原话会显示在对话里）
- 「**继续调整计划**」→ 收起批准条并聚焦输入框，继续在计划模式里改

> 切换会话 / 新建对话 / 停止生成都会清除待批准的批准条，避免批准到旧计划。
> 手动切到开发模式也会作废待批准状态。

**其它便捷操作**
- 右下角「⬆ 回到底部」：往上翻看历史后出现，一键回到最新消息
- 右下角「我的消息 N」：列出你发过的全部提问，点哪条跳到哪条

## 内置工具一览

| 工具 | 作用 | 范围 |
|---|---|---|
| `list_files` | 浏览目录（可递归，深度≤4） | 工作区 + 只读目录 |
| `read_file` | 读文本文件（按行截取，≤64KB） | 工作区 + 只读目录 |
| `edit_file` | 精确替换编辑（old/new 唯一匹配，改文件优先用它） | 仅项目工作区 |
| `write_file` | 写文件（覆盖式，≤256KB，自动建目录，新建文件用） | 仅项目工作区 |
| `search_files` | 全文搜索（忽略依赖目录，≤100 条） | 工作区 + 只读目录 |
| `todo_write` | 维护会话待办清单（实时展示，压缩后保留） | 智能体内部状态 |
| `memory_append` / `memory_read` | 长期记忆读写（全局 + 项目两级，跨会话保留） | data/memory |
| `run_command` | 白名单命令 | 仅项目工作区 |
| `mcp_<server>_<tool>` | 已连接 MCP server 的工具 | 由该 server 决定 |

未选项目时，写文件与命令不可用（小柯会提醒你先建项目）。工具循环上限默认 8 轮（设置可改 1-32），超限后强制模型给出最终答复。

**项目规则与长期记忆**
- 项目根（及只读授权目录）下的 `AGENTS.md` / `CLAUDE.md` 等规则文件会自动发现并注入系统提示词（内容做防伪标签转义，单文件 16KB / 总量 32KB 上限）
- 长期记忆存于 `data/memory/`：`MEMORY.md`（全局）+ `projects/<id>/MEMORY.md`（项目级），每次对话注入预算内内容；设置 → 高级可手工整理全局记忆

## API 一览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查（mode/model） |
| GET/PUT | `/api/settings` | 设置读写（Key 脱敏；PUT 热生效并重排 MCP 进程） |
| POST | `/api/settings/test` | 测试上游连通 + 拉模型列表 |
| GET | `/api/models` | 当前配置的模型列表 |
| GET | `/api/mcp/status` | MCP server 运行状态 |
| POST | `/api/mcp/retry` | 重启退出/启动失败的 MCP server（正在运行的不动） |
| GET/POST | `/api/projects` | 项目列表 / 新建（支持模板与自定义目录） |
| DELETE | `/api/projects/{id}` | 删除项目（**连带删除目录内全部文件**） |
| GET | `/api/projects/{id}/tree` | 项目文件树 |
| GET/POST/PUT/DELETE | `/api/agents[/{id}]` | 智能体 CRUD（名字/头像/人格/工具开关）；内置小柯的 PUT/DELETE 返回 403 |
| POST | `/api/agents/{id}/toggle` | 智能体工具开关（内置小柯返回 403） |
| GET/POST/PUT/DELETE | `/api/skills[/{id}]` | 技能 CRUD |
| POST | `/api/skills/{id}/toggle` | 技能启用/停用 |
| POST | `/api/upload/avatar` | 头像上传（PNG/JPEG/WebP/GIF ≤2MB） |
| GET/POST | `/api/sessions`、GET/DELETE `/api/sessions/{id}` | 会话 CRUD（带 projectId + agentId 绑定） |
| PUT | `/api/sessions/{id}/title` | 重命名会话（空标题 = 清空并回退自动命名） |
| GET | `/api/sessions/{id}/export` | 导出单个会话为 JSON 附件（含未合并日志） |
| GET | `/api/backup/sessions` | 下载全部会话的 ZIP 备份（含 manifest.json） |
| GET | `/api/trash` | 回收站列表（标题、删除时间、消息数、大小） |
| POST | `/api/trash/{dir}/restore` | 从回收站恢复一条会话（同 ID 仍在时返回 409） |
| DELETE | `/api/trash/{dir}` | 彻底删除回收站里的一条记录 |
| GET/PUT | `/api/memory` | 全局长期记忆读取/覆写（设置页「高级」编辑框） |
| POST | `/api/chat` | 流式对话（SSE：start/delta/tool/tool_result/todo/done/error，请求可带 agentId/reasoning；done 携带本次 token 用量） |

## 目录结构

```
小柯智能体/
├── main.go                  # 入口与装配（含智能体/技能种子）
├── internal/
│   ├── config/              # 启动配置（config.json + 环境变量）
│   ├── settings/            # 运行时设置（data/settings.json，热更新）
│   ├── persona/             # 默认小柯人设常量
│   ├── prompt/              # 系统提示词组装（智能体人格+技能+项目+工具规范）
│   ├── agents/              # 智能体仓库（data/agents.json）
│   ├── skills/              # 技能仓库（data/skills.json，注入系统提示词）
│   ├── chat/                # agent 工具调用循环 + OpenAI 流式 + mock + 历史裁剪/清理
│   ├── tools/               # 内置工具（路径安全/白名单/超时/截断）
│   ├── mcp/                 # MCP stdio 客户端 + 常驻进程 Manager
│   ├── projects/            # 项目工作区（索引 data/projects.json）
│   ├── store/               # 会话存储（.json 快照 + .log 追加 + .meta.json 摘要）
│   └── api/                 # 设置/项目/MCP/智能体/技能/头像上传路由
├── config.json.example      # 启动配置模板
├── assets/avatar.png        # 默认智能体头像（灰原哀）
├── assets/avatars/          # 上传的智能体头像
├── projects/                # 默认项目根目录
├── data/                    # settings/skills/agents/projects/sessions（gitignored）
├── start.bat / start.sh
└── web/                     # Vue 3 前端
    └── src/
        ├── components/      # SideBar / ChatView / MessageItem / Composer /
        │                    # SettingsView / AgentSection / SkillSection /
        │                    # ProjectModal / AvatarBadge
        └── lib/             # api.ts（SSE+REST）、markdown.ts
```

## 测试

```bash
go test -race ./internal/...   # 工具路径逃逸/符号链接逃逸、文件读写、白名单、cwd 缺省、
                               # settings 空切片/Key 语义、projects 并发创建、
                               # agents/skills CRUD 与种子、
                               # MCP 握手/调用/并发（内建假 server）、ToolDef 序列化、
                               # 只读工具过滤、历史裁剪与孤立 tool 清理、会话锁、
                               # 快照/日志存储与合并、静态资源目录列表与安全响应头
```

CI：`.github/workflows/ci.yml`（push / PR 自动跑 `go vet`、`go test -race`、`npm ci` + typecheck + build）。

已验证（2026-10-30 回归）：
- **存储结构**：追加只写日志不重写快照；日志超阈值自动合并；无 meta 的旧会话可读可继续追加；50 路并发追加不丢消息
- **符号链接**：指向工作区外的链接被拒绝（Linux CI 覆盖；本机 Windows 无符号链接权限时跳过）
- **静态资源**：`/assets/` 目录不可列出，文件正常返回；CSP/nosniff/X-Frame-Options 均存在
- **历史裁剪**：按 maxHistory + 256KB 字节预算裁剪，并从 user 边界开始；115 条会话模拟不再出现「以 tool 开头」
- **孤立 tool 清理**：中断留下的不完整 tool_calls / tool 结果会在发送前被清掉
- **MCP 并发**：8 路并发 `echo` 调用结果全部按 ID 正确分发（回归旧的并发 Scan 问题）
- **MCP 只读**：`readOnly=true` 的 server 只暴露读工具，`write_file`/`edit_file`/`create_directory` 被过滤
- **会话锁**：同一会话两个并发请求，一个 200、一个 409「该会话正在生成回复」
- **命令退出码**：`cmd /c exit 1` 返回错误，前端标红
- **设置往返**：MCP 参数含空格路径、`env`、`readOnly` 保存后原样保留
- **临时实例 E2E**：mock 对话、工具写文件、`/api/mcp/retry`、静态页面均正常
- 2026-10-29 的回归项仍然有效：真实模型工具调用、MCP 读代码、停止语义、MCP 冷启动

## 安全与限制

- **默认只监听 `127.0.0.1`**，所有 API 无鉴权。改 `XIAOKE_ADDR=0.0.0.0` 会把模型 Key 和文件工具暴露给同网段——对外请套反代 + 鉴权。
- **写文件 / 命令被限制在项目工作区内**（路径归一化 + 符号链接解析 + 越界拒绝），但**没有逐次审批门**：接了真实模型后小柯可以直接改你项目里的文件、跑白名单命令。重要项目先提交 git 再让它动手。
- **MCP 的「只读」是工具暴露层过滤**：一键添加的 filesystem server 默认勾选只读，写类工具不会给模型；取消勾选后它对授权目录可读写。过滤不等于 OS 权限隔离，第三方 server 仍以你的用户权限运行。
- **同一会话串行处理**：同时只允许一个生成请求，第二个会收到 409「该会话正在生成回复」。
- **历史会裁剪**：按 `maxHistory` 条数 + 256KB 字节预算，从最近的 user 消息开始；被中断的工具轮会在下次请求前清理，不会把非法序列发给上游。配置了「上下文窗口」后，自动压缩按 token 水位（bytes/4 估算）触发；摘要带退化重试与缩减率守卫（摘要太长就放弃本次压缩）。
- **项目规则文件与长期记忆属于第三方/模型输入**：AGENTS.md 等规则文件内容会做伪框架标签转义（无法伪造系统消息），但文件内容仍会被注入提示词——别把不可信的仓库规则文件随便放进项目根。
- **命令非 0 退出会标为失败**（红色），输出仍保留在工具结果里。
- **会话存储是快照 + 追加日志**：每条消息只追加一行 `<id>.log`，侧栏只读 `<id>.meta.json`；日志超过 1MB 自动合并回 `<id>.json` 快照。备份/迁移请把 `data/sessions/` 整个目录一起带走。
- **计划批准条**：只有在回答里出现「实施步骤 / 风险与边界」或编号步骤时才出现，模型反问或闲聊不会误触发。
- **静态资源**：`/assets/` 不允许目录列表；所有响应带 CSP、nosniff、X-Frame-Options、Referrer-Policy 等基本安全头。
- 点「停止」：未完成的回复不落库，本地保留半截并标注「已停止」。
- **MCP server 采用异步启动**：保存设置立即返回，状态页显示「启动中/运行中/失败」；npx 类 server 首次冷启动可能需要 30-60 秒（握手上限 90 秒），失败会在状态里显示 server 报错；「刷新并重试」可重新拉起。
- 头像来自名侦探柯南官方 Wiki，**仅供个人本地使用**。`config.json`、`data/settings.json`（含 Key）与 `data/` 已 gitignore，勿外传。

## 数据安全（备份 / 回收站 / 导出）

小柯对会话数据做了三层保护，避免误删或崩溃后不可恢复：

| 机制 | 位置 | 说明 |
|---|---|---|
| 自动备份 | `data/backups/sessions-*.zip` | 服务启动时立刻备份一次，之后每 24 小时一次，自动只保留最近 **14** 份 |
| 删除回收站 | `data/sessions/trash/<id>-<时间戳>/` | 每次删除会话前先复制快照/日志/摘要；复制失败则**取消删除**并报错。保留 **30 天**，过期自动清理 |
| 手动导出 | 侧栏会话悬停的下载按钮 / 设置 → 高级 | 单会话 JSON：`GET /api/sessions/{id}/export`；全量 ZIP：`GET /api/backup/sessions` |

恢复方式：

- **界面恢复（推荐）**：设置 → 高级 → 回收站，列出每条被删会话的标题、删除时间、消息数和大小，点「恢复」即可回到侧栏；点「彻底删除」才会真正移除。
- 单个会话（手工）：从 `data/sessions/trash/<id>-<时间戳>/` 把 `<id>.json`、`<id>.log`、`<id>.meta.json` 复制回 `data/sessions/`，重启即可在侧栏看到。
- 整库回滚：停服务，解压 `data/backups/sessions-<时间戳>.zip` 覆盖到 `data/sessions/`（zip 内含 `manifest.json` 记录会话清单）。
- 备份目录和回收站都在 `data/` 下，已 gitignore；迁移或换机时连同 `data/sessions/` 一起带走。

> 同 ID 的会话仍然存在时，恢复会返回 409 而不是覆盖——避免把新会话冲掉。

## 许可

代码以 MIT 许可发布（见 `LICENSE`）。`assets/avatar.png` 来自名侦探柯南官方 Wiki，版权归原作者，不在 MIT 许可范围内，仅供个人本地使用。
