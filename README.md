# 小柯 · 独立网页版智能体

以「小柯」为名的个人 AI 助手：界面仿 [opensquilla](https://github.com/TokenRhythm/opensquilla) 的暗色控制台风格（石墨底 + 品牌橙），头像为灰原哀，每条最终答复以粗体「**小柯**」开头。

**能做什么：**

- 💬 流式对话（Markdown / 代码高亮 / 工具过程可视化 / 左右分区气泡）
- 🤖 **多智能体**：设置页可新建多个智能体（名字、头像 emoji 或上传图片、人格提示词、工具开关），侧栏一键切换，会话自动绑定所选智能体
- 📚 **技能系统**：写一条 Markdown 技能（如「金字塔写作」），启用后注入系统提示词，随时生效
- ⚙️ **设置中心**：整页导航（智能体 / 技能 / 模型 / MCP / 目录 / 高级），模型 API 界面配置热生效、一键测试连接拉模型列表
- 🔌 **MCP 工具**：内置标准 MCP 客户端（stdio），可添加任意 MCP server；一键添加官方 filesystem 只读服务看代码
- 📁 **项目工作区**：新建项目（代码/文章模板），小柯在项目里读写文件、跑白名单命令，真正帮你写代码、写文章
- 🧠 **思考程度**：对话页右下角随时调节（关/极简/低/中/高），浏览器记住选择

- **后端**：Go 1.22+ 纯标准库（零第三方依赖），SSE + 工具调用循环 + MCP 客户端全部手写
- **前端**：Vue 3 + Vite + TypeScript，marked + DOMPurify + highlight.js
- **模型**：任意 OpenAI 兼容上游（DeepSeek / 本地 opencode 网关 / OpenCode Zen…），内置免 Key 的 mock 演示模式（可模拟一次工具调用，验证全链路）

## 快速开始

双击 `start.bat`（首次自动 npm install + build + go build，然后开浏览器），或手动：

```bat
cd web && npm install && npm run build && cd ..
go build -o xiaoke.exe .
xiaoke.exe
```

打开 **http://127.0.0.1:18790**。默认 mock 演示模式，无需 API Key；在项目里说「帮我写一个文件」可以看到完整的工具调用演示。

前端热更新开发：`cd web && npm run dev`（:5175，/api、/assets、/healthz 自动代理）。

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

- 在「授权小柯只读查看的目录」里填绝对路径（如你想让小柯读的代码仓库），点 **＋ 添加 filesystem 只读服务** —— 会自动生成一条 npx 版官方 filesystem server 配置
- 也可以「手动添加其它 MCP 服务」（name / command / args，空格分隔），与 opensquilla 的 `[[mcp.servers]]` 形态一致
- 保存后服务作为常驻进程启动；「刷新状态」能看到 ● 运行中 · N 个工具

之后接上真实模型，就可以直接问「看看 opencode_set 的路由是怎么写的」这类问题，小柯会自己调 MCP 工具去读代码。

> 注意：官方 filesystem server 对授权目录是**可读写**的，授权前想清楚范围；只是想让它「看」就授权尽量小的目录。

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
| `write_file` | 写文件（覆盖式，≤256KB，自动建目录） | 仅项目工作区 |
| `search_files` | 全文搜索（忽略依赖目录，≤100 条） | 工作区 + 只读目录 |
| `run_command` | 白名单命令 | 仅项目工作区 |
| `mcp_<server>_<tool>` | 已连接 MCP server 的工具 | 由该 server 决定 |

未选项目时，写文件与命令不可用（小柯会提醒你先建项目）。工具循环上限默认 8 轮（设置可改 1-32），超限后强制模型给出最终答复。

## API 一览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查（mode/model） |
| GET/PUT | `/api/settings` | 设置读写（Key 脱敏；PUT 热生效并重排 MCP 进程） |
| POST | `/api/settings/test` | 测试上游连通 + 拉模型列表 |
| GET | `/api/models` | 当前配置的模型列表 |
| GET | `/api/mcp/status` | MCP server 运行状态 |
| GET/POST | `/api/projects` | 项目列表 / 新建（支持模板与自定义目录） |
| DELETE | `/api/projects/{id}` | 删除项目（**连带删除目录内全部文件**） |
| GET | `/api/projects/{id}/tree` | 项目文件树 |
| GET/POST/PUT/DELETE | `/api/agents[/{id}]` | 智能体 CRUD（名字/头像/人格/工具开关） |
| POST | `/api/agents/{id}/toggle` | 智能体工具开关 |
| GET/POST/PUT/DELETE | `/api/skills[/{id}]` | 技能 CRUD |
| POST | `/api/skills/{id}/toggle` | 技能启用/停用 |
| POST | `/api/upload/avatar` | 头像上传（PNG/JPEG/WebP/GIF ≤2MB） |
| GET/POST | `/api/sessions`、GET/DELETE `/api/sessions/{id}` | 会话 CRUD（带 projectId + agentId 绑定） |
| POST | `/api/chat` | 流式对话（SSE：start/delta/tool/tool_result/done/error，请求可带 agentId/reasoning） |

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
│   ├── chat/                # agent 工具调用循环 + OpenAI 流式 + mock 演示
│   ├── tools/               # 内置工具（路径安全/白名单/超时/截断）
│   ├── mcp/                 # MCP stdio 客户端 + 常驻进程 Manager
│   ├── projects/            # 项目工作区（索引 data/projects.json）
│   ├── store/               # 会话存储（data/sessions/*.json，含工具消息与智能体绑定）
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
go test -race ./internal/...   # 工具路径逃逸、文件读写、白名单、cwd 缺省、
                               # settings 空切片/Key 语义、projects 并发创建、
                               # agents/skills CRUD 与种子、
                               # MCP 握手/调用（内建假 server）、ToolDef 序列化
```

已验证（2026-10-29 回归）：
- **真实模型工具调用**：`zai/glm-5.3-flash` → `write_file` 真实落盘 → 回填 → 继续生成 → done
- **真实模型 + MCP 读代码**：模型自主调 `mcp_fs-opencode_list_directory`/`read_text_file`，遇权限拒绝自己换路径，给出带「小柯」前缀的总结
- **run_command 白名单**：`git --version` cwd 缺省直接跑项目根
- **停止语义**：中途断开后服务端只存 user 消息，半截回复不落库
- **MCP 冷启动**：npx 首次下载慢启动时保存设置秒回，状态页显示「启动中」，90 秒内就绪
- mock 工具闭环、设置热更新、项目 CRUD/边界、非法输入拒绝

## 安全与限制

- **默认只监听 `127.0.0.1`**，所有 API 无鉴权。改 `XIAOKE_ADDR=0.0.0.0` 会把模型 Key 和文件工具暴露给同网段——对外请套反代 + 鉴权。
- **写文件 / 命令被限制在项目工作区内**（路径归一化 + 越界拒绝），但**没有逐次审批门**：接了真实模型后小柯可以直接改你项目里的文件、跑白名单命令。重要项目先提交 git 再让它动手。
- **filesystem MCP server 对授权目录可读写**，授权范围尽量小。
- **同一会话并发对话未加会话级写锁**（单人使用无碍）。
- 点「停止」：未完成的回复不落库，本地保留半截并标注「已停止」。
- **MCP server 采用异步启动**：保存设置立即返回，状态页显示「启动中/运行中/失败」；npx 类 server 首次冷启动可能需要 30-60 秒（握手上限 90 秒），失败会在状态里显示 server 报错。
- 头像来自名侦探柯南官方 Wiki，**仅供个人本地使用**。`config.json`、`data/settings.json`（含 Key）与 `data/` 已 gitignore，勿外传。
