<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from './lib/api'
import type { Agent, CustomCommand, Message, MsgGroup, Project, SessionMeta, Skill, Todo, ToolActivity, ToolPair, Turn } from './types'
import type { UsageInfo } from './lib/api'
import SideBar from './components/SideBar.vue'
import ChatView from './components/ChatView.vue'
import Composer from './components/Composer.vue'
import SettingsView from './components/SettingsView.vue'
import ProjectModal from './components/ProjectModal.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'
import AskDialog from './components/AskDialog.vue'
import Icon from './components/Icon.vue'
import { askConfirm } from './lib/confirm'

const sessions = ref<SessionMeta[]>([])
const currentId = ref('')
const messages = ref<Message[]>([])
const greeting = ref('**小柯** 你好，我是小柯。有什么想问的，尽管说。')
const emptyProjectHint = ref('')
const streaming = ref(false)
const streamingText = ref('')
const liveTools = ref<ToolActivity[]>([])
// 上下文压缩提示：压缩发生后短暂显示，让「模型为什么记得概括版历史」有解释
const compactNotice = ref('')
let compactTimer: number | undefined
const error = ref('')
const mode = ref('')
const model = ref('')

// 当前会话的待办清单与最近一次用量（todo/usage SSE 事件维护，切会话随会话还原）
const sessionTodos = ref<Todo[]>([])
const lastUsage = ref<UsageInfo | null>(null)

// ask 交互（ask_user 提问 / ask 模式审批）：SSE ask 事件 → 弹窗 → POST 回答
interface AskRequest {
  id: string
  kind: 'question' | 'permission'
  question?: string
  options?: string[]
  multiSelect?: boolean
  tool?: string
  summary?: string
  resolve: (answer: string) => void
}
const askQueue = ref<AskRequest[]>([])
// AskDialog 一次只展示队首；并行工具/子代理可同时提问，排队逐个回答
const askRequest = computed<AskRequest | null>(() => askQueue.value[0] ?? null)
function handleAsk(req: AskRequest) {
  askQueue.value.push(req)
}
function handleAskAnswer(answer: string) {
  const req = askQueue.value.shift()
  if (!req) return
  req.resolve(answer) // POST 投递由 api.ts 的 SSE 循环统一处理
}
function clearAskQueue() {
  // 流结束/出错：未决提问全部按「跳过」结清，弹窗不残留
  for (const req of askQueue.value) req.resolve('')
  askQueue.value = []
}

// 上游重试横幅：retry SSE 事件驱动，展示几秒后自动消失
const retryNotice = ref('')
let retryTimer: number | undefined

// 消息队列：流式输出期间继续输入的消息先进队列，当前轮结束后自动逐条发送
//（对标 opencode 的 queue 模式；steer 需要后端 SSE 多路复用，收益不成比例，不做）
interface QueuedMsg {
  content: string
  hidden?: boolean
}
const sendQueue = ref<QueuedMsg[]>([])
const QUEUE_CAP = 200
let draining = false
function drainQueue() {
  if (draining) return
  const next = sendQueue.value.shift()
  if (!next) return
  draining = true
  handleSend(next.content, { hidden: next.hidden }).finally(() => {
    draining = false
    // 队列里还有？下一帧继续取（避免深递归）
    if (sendQueue.value.length) window.setTimeout(drainQueue, 50)
  })
}
/** 清空队列（切会话/项目/智能体时必须：排队消息属于原上下文，漏到新会话就是数据错位） */
function clearSendQueue() {
  sendQueue.value = []
}
function flashNotice(text: string) {
  compactNotice.value = text
  if (compactTimer) window.clearTimeout(compactTimer)
  compactTimer = window.setTimeout(() => (compactNotice.value = ''), 6000)
}

// 自定义斜杠命令（设置 → 高级维护）：Composer 补全与模板展开
const slashCommands = ref<CustomCommand[]>([])
async function refreshCommands() {
  try {
    const s = await api.getSettings()
    slashCommands.value = s.commands || []
  } catch {
    /* 忽略 */
  }
}

// 智能体
const agents = ref<Agent[]>([])
const currentAgentId = ref('')
const currentAgent = computed<Agent | null>(
  () => agents.value.find((a) => a.id === currentAgentId.value) || agents.value[0] || null,
)

// 项目
const projects = ref<Project[]>([])
const currentProjectId = ref('')
const currentProject = computed<Project | null>(
  () => projects.value.find((p) => p.id === currentProjectId.value) || null,
)

// 技能列表由设置页内部管理；此处仅保留类型引用避免未用告警
const _skillsTypeRef = ref<Skill[]>([])

// 视图与弹窗
const settingsOpen = ref(false)
const projectModalOpen = ref(false)

// 思考程度（右下角选择，浏览器本地记住，下一条消息生效）
const REASONING_KEY = 'xiaoke.reasoning'
const reasoning = ref(localStorage.getItem(REASONING_KEY) || 'none')

// 工作模式：plan（先调研出计划，只读）⇄ build（开发，完整能力）
const MODE_KEY = 'xiaoke.mode'
type WorkMode = 'plan' | 'build' | 'ask'
const workMode = ref<WorkMode>((localStorage.getItem(MODE_KEY) as WorkMode) || 'build')
function setWorkMode(m: WorkMode) {
  workMode.value = m
  try {
    localStorage.setItem(MODE_KEY, m)
  } catch {
    /* 隐私模式忽略 */
  }
}

// 计划模式产出计划后，等待用户批准（在最后一个回合末尾显示批准条）
const planAwaitingApproval = ref(false)

function clearApproval() {
  planAwaitingApproval.value = false
}

// 切到开发模式即视为已离开计划流程，批准条作废（避免切回计划时出现过期的待批准）
function onModeChange(m: WorkMode) {
  setWorkMode(m)
  if (m === 'build') clearApproval()
}

/** 批准计划：切开发模式 → 自动发送「开始」按计划开工（该消息不显示在对话里）。
 *  流式进行中不再静默丢弃——handleSend 会把它排队，轮到时自动开工。 */
function handleApprove() {
  clearApproval()
  setWorkMode('build')
  handleSend('开始', { hidden: true })
}

/** 批准但用自己的实现方式：切开发模式 → 发送用户写的指令（正常显示） */
function handleApproveCustom(text: string) {
  const t = text.trim()
  if (!t) return
  clearApproval()
  setWorkMode('build')
  handleSend(t)
}

/** 继续调整计划：收起批准条并聚焦输入框 */
function handleAdjustPlan() {
  clearApproval()
  const ta = document.querySelector<HTMLTextAreaElement>('.composer textarea')
  ta?.focus()
}
// 当前会话 ID（刷新后恢复到上次的会话）
const CURRENT_SESSION_KEY = 'xiaoke.currentSession'
function rememberSession(id: string) {
  try {
    if (id) localStorage.setItem(CURRENT_SESSION_KEY, id)
    else localStorage.removeItem(CURRENT_SESSION_KEY)
  } catch {
    /* 隐私模式下可能禁用，忽略 */
  }
}
function setReasoning(v: string) {
  reasoning.value = v
  try {
    localStorage.setItem(REASONING_KEY, v)
  } catch {
    /* 隐私模式下可能禁用，忽略 */
  }
}

let controller: AbortController | null = null
let abortedByUser = false

async function refreshSessions() {
  try {
    const list = await api.listSessions()
    sessions.value = list.sessions
    if (list.greeting) greeting.value = list.greeting
    if (list.emptyProjectHint) emptyProjectHint.value = list.emptyProjectHint
  } catch {
    /* 侧边栏刷新失败不打断主流程 */
  }
}

async function refreshProjects() {
  try {
    const r = await api.listProjects()
    projects.value = r.projects
    // 当前项目被删除时解绑
    if (currentProjectId.value && !r.projects.some((p) => p.id === currentProjectId.value)) {
      currentProjectId.value = ''
    }
  } catch {
    /* 忽略 */
  }
}

async function refreshAgents() {
  try {
    const r = await api.listAgents()
    agents.value = r.agents
    if (!currentAgentId.value && r.agents.length) {
      currentAgentId.value = r.agents[0].id
    }
    // 当前智能体被删除时回退
    if (currentAgentId.value && !r.agents.some((a) => a.id === currentAgentId.value)) {
      currentAgentId.value = r.agents[0]?.id || ''
    }
  } catch {
    /* 忽略 */
  }
}

async function refreshHealth() {
  try {
    const h = await api.health()
    mode.value = h.mode
    model.value = h.model
  } catch {
    mode.value = '离线'
  }
}

onMounted(async () => {
  refreshProjects()
  refreshAgents()
  refreshHealth()
  refreshCommands()
  await refreshSessions()
  // 恢复上次的会话（刷新后不丢）
  try {
    const last = localStorage.getItem(CURRENT_SESSION_KEY)
    if (last && sessions.value.some((s) => s.id === last)) {
      const s = await api.getSession(last)
      currentId.value = s.id
      currentProjectId.value = s.projectId || ''
      if (s.agentId) currentAgentId.value = s.agentId
      messages.value = s.messages
      sessionTodos.value = s.todos || []
    } else if (last) {
      localStorage.removeItem(CURRENT_SESSION_KEY)
    }
  } catch {
    /* 恢复失败则回到欢迎页 */
  }
})

// 切换智能体 = 开新对话（当前会话已绑定旧智能体，不混用）
function handleSelectAgent(id: string) {
  if (currentAgentId.value === id && !currentId.value) return
  if (streaming.value) handleStop()
  clearApproval()
  clearSendQueue()
  currentAgentId.value = id
  currentId.value = ''
  rememberSession('')
  messages.value = []
  sessionTodos.value = []
  lastUsage.value = null
  error.value = ''
}

/**
 * 回合模型（TurnView 渲染）：
 * - 一条 user 消息开启一个回合，回合内所有 assistant/tool 消息归入该回合
 * - 回合内再按「轮」切分组：每轮工具调用自成一块（>14 次再分块），过渡正文与首批结果同块，
 *   最终答复独立成纯正文块
 * - 回合带首末消息时间（工作时长），流式中末时间为空 → TurnView 实时走秒
 */
const turns = computed<Turn[]>(() => {
  const out: Turn[] = []
  let cur: Turn | null = null
  let round: { sections: string[]; tools: ToolPair[] } | null = null
  let roundNo = 0
  const PER_BLOCK = 14

  const flushRound = () => {
    if (!cur || !round) return
    // 同一轮工具调用超过 14 次会被拆成多块，但共享同一个 round 编号
    const thisRound = roundNo++
    if (round.tools.length === 0) {
      if (round.sections.length) {
        cur.groups.push({ kind: 'assistant', sections: round.sections, tools: [], round: thisRound })
      }
    } else {
      const total = Math.ceil(round.tools.length / PER_BLOCK)
      let n = 0
      for (let i = 0; i < round.tools.length; i += PER_BLOCK) {
        n++
        const chunk = round.tools.slice(i, i + PER_BLOCK)
        for (const t of chunk) t.id = `blk-${n}/${total}|${t.id}`
        cur.groups.push({ kind: 'assistant', sections: i === 0 ? round.sections : [], tools: chunk, round: thisRound })
      }
    }
    round = null
  }

  const touchTime = (t: Turn, iso?: string) => {
    if (!iso) return
    if (!t.startTime) t.startTime = iso
    t.endTime = iso
  }

  for (let mi = 0; mi < messages.value.length; mi++) {
    const m = messages.value[mi]
    if (m.role === 'user') {
      flushRound()
      // 隐藏消息（如批准计划的自动指令）不显示问题气泡，但仍开启一个新回合承接回复
      cur = { question: m.hidden ? '' : m.content, groups: [], startTime: m.createdAt, msgIndex: mi }
      roundNo = 0
      out.push(cur)
      continue
    }
    if (!cur) {
      // 历史数据可能缺 user 头：兜底建空回合
      cur = { question: '', groups: [] }
      roundNo = 0
      out.push(cur)
    }
    touchTime(cur, m.createdAt)
    if (!round) {
      round = { sections: [], tools: [] }
    }
    if (m.role === 'assistant') {
      const hasCalls = (m.toolCalls?.length ?? 0) > 0
      const hasText = !!m.content && m.content.trim() !== ''
      if (hasCalls && hasText && round.tools.length > 0) {
        flushRound()
        round = { sections: [], tools: [] }
      } else if (!hasCalls && hasText && round.tools.length > 0) {
        flushRound()
        round = { sections: [], tools: [] }
      }
      if (hasCalls) {
        for (const c of m.toolCalls!) {
          round.tools.push({ id: c.id, name: c.name, arguments: c.arguments })
        }
      }
      if (hasText) {
        round.sections.push(m.content)
      }
      // 用户停止的回合：标记到回合上，让 TurnView 区分「已停止」与「空回复」
      if (m.stopped && cur) cur.stopped = true
    } else if (m.role === 'tool') {
      const pair = round.tools.find((t) => t.id === m.toolCallId)
      if (pair) {
        pair.result = m.content
        pair.ok = !/工具执行出错|路径不在|路径不能为空|不在白名单|尚未选择项目|命令退出码非 0/.test(m.content)
      } else {
        round.tools.push({ id: m.toolCallId || '', name: m.name || 'tool', arguments: '', result: m.content, ok: true })
      }
    }
  }
  flushRound()

  // 流式：实时进度并入最后一个回合（无回合则自建），结束时间留空 → TurnView 实时走秒
  if (streaming.value || error.value) {
    if (!cur) {
      cur = { question: '', groups: [] }
      out.push(cur)
    }
    cur.streaming = streaming.value
    if (!cur.startTime) cur.startTime = new Date().toISOString()
    cur.endTime = undefined
    const tools: ToolPair[] = liveTools.value.map((t, i) => ({
      id: `live-${i}`,
      name: t.name,
      arguments: t.args || '',
      result: t.done ? [t.error, t.preview].filter(Boolean).join(' | ') : undefined,
      ok: t.done ? t.ok : undefined,
      diff: t.diff,
    }))
    cur.groups.push({
      kind: 'live',
      sections: streamingText.value ? [streamingText.value] : [],
      tools,
      round: roundNo,
      streaming: true,
      error: streaming.value ? undefined : error.value || undefined,
    })
  }
  return out
})

async function handleSend(content: string, opts?: { hidden?: boolean }) {
  // 流式进行中：消息进入队列，当前轮结束后自动发送（Composer 的排队按钮/Enter 都会走到这里）
  if (streaming.value) {
    if (sendQueue.value.length >= QUEUE_CAP) {
      flashNotice(`排队消息已达 ${QUEUE_CAP} 条上限，这条没有加入队列——请等当前回复结束后再发`)
      return
    }
    sendQueue.value.push({ content, hidden: opts?.hidden })
    return
  }
  error.value = ''
  // 新一轮开始：旧的批准条作废；记录本次发送的模式
  clearApproval()
  const sentMode = workMode.value
  messages.value.push({ role: 'user', content, hidden: opts?.hidden === true })
  streaming.value = true
  streamingText.value = ''
  liveTools.value = []
  controller = new AbortController()
  abortedByUser = false
  try {
    let sessionId = currentId.value
    if (!sessionId) {
      const s = await api.createSession(currentProjectId.value, currentAgent.value?.id)
      sessionId = s.id
      rememberSession(s.id)
    }
    const r = await api.chat({
      sessionId,
      content,
      projectId: currentProjectId.value,
      agentId: currentAgent.value?.id || '',
      reasoning: reasoning.value,
      mode: workMode.value,
      approve: opts?.hidden === true,
      signal: controller.signal,
      onDelta: (t) => {
        streamingText.value += t
      },
      onCompact: (covered) => {
        compactNotice.value = `上下文已自动压缩：较早的 ${covered} 条消息已总结为摘要（完整历史仍在本地，可在设置关闭自动压缩）`
        if (compactTimer) window.clearTimeout(compactTimer)
        compactTimer = window.setTimeout(() => (compactNotice.value = ''), 8000)
      },
      onToolStart: (name, args, id) => {
        liveTools.value.push({ id, name, args, done: false })
      },
      onToolResult: (info) => {
        // 并行执行时按调用 id 配对；无 id（旧后端）退回「最近一个同名未完成」
        const list = liveTools.value
        let idx = -1
        if (info.id) {
          idx = list.findIndex((t) => t.id === info.id && !t.done)
        }
        if (idx < 0) {
          for (let i = list.length - 1; i >= 0; i--) {
            const t = list[i]
            if (t.name === info.name && !t.done) {
              idx = i
              break
            }
          }
        }
        if (idx >= 0) {
          const t = list[idx]
          t.done = true
          t.ok = info.ok
          t.preview = info.preview
          t.error = info.error
          t.ms = info.ms
          t.diff = info.diff
        }
      },
      onRetry: (info) => {
        retryNotice.value = `上游临时故障，${Math.round(info.delayMs / 1000)} 秒后自动重试（第 ${info.attempt}/${info.maxAttempts} 次）`
        if (retryTimer) window.clearTimeout(retryTimer)
        retryTimer = window.setTimeout(() => (retryNotice.value = ''), Math.min(info.delayMs + 2500, 15000))
      },
      onTodo: (todos) => {
        sessionTodos.value = todos
      },
      onAsk: (req) => {
        // SSE ask 事件：挂起等待用户操作（返回的 promise 由按钮点击 resolve）
        return new Promise<string>((resolve) => {
          handleAsk({ ...req, resolve })
        })
      },
      onDone: (usage) => {
        lastUsage.value = usage || null
      },
    })
    clearAskQueue()
    // 无论正常结束还是中途停止，都以服务端落库为准刷新一次
    let serverHadThis = false
    if (r.sessionId === currentId.value || !currentId.value) {
      currentId.value = r.sessionId
      rememberSession(r.sessionId)
      try {
        const s = await api.getSession(r.sessionId)
        if (r.sessionId === currentId.value) {
          messages.value = s.messages
          sessionTodos.value = s.todos || []
          const last = s.messages[s.messages.length - 1]
          serverHadThis = last?.role === 'assistant' && !!last.content && last.content === streamingText.value
        }
      } catch {
        /* 拉取失败时保留本地流式文本 */
        if (streamingText.value) {
          messages.value.push({ role: 'assistant', content: streamingText.value })
          serverHadThis = true
        }
      }
    }
    // 中途停止且服务端没保存这条内容：本地保留半截并标记「已停止」。
    // 首 token 前就停止时内容为空，也要标记，否则界面会误显示「没有收到内容，请重试」。
    if (abortedByUser && !serverHadThis && r.sessionId === currentId.value) {
      messages.value.push({ role: 'assistant', content: streamingText.value, stopped: true })
    }
    // 计划模式下，只有回答看起来是结构化计划时才出现批准条；
    // 模型反问/闲聊时不误判（用户仍可手动切开发模式）。
    if (sentMode === 'plan' && !abortedByUser && !error.value) {
      const lastAssistant = [...messages.value]
        .reverse()
        .find((m) => m.role === 'assistant' && (m.content || '').trim())
      const text = lastAssistant?.content || ''
      const looksLikePlan = /实施步骤|风险与边界/.test(text) || /(^|\n)\s*1[.、]/.test(text)
      if (looksLikePlan) planAwaitingApproval.value = true
    }
    streamingText.value = ''
    liveTools.value = []
  } catch (e) {
    error.value = (e as Error).message || '出错了，请稍后再试'
  } finally {
    clearAskQueue() // 流已结束/出错：未决的提问弹窗全部结清，不残留
    streaming.value = false
    streamingText.value = ''
    liveTools.value = []
    controller = null
    // 无论成功/停止/出错都刷新会话列表：会话已落库，必须出现在侧栏（持久化可见）
    refreshSessions()
    refreshCommands()
    // 自动起标题在服务端异步完成（晚于本次刷新），稍后再刷一次捡回新标题
    if (mode.value !== 'mock') window.setTimeout(refreshSessions, 8000)
    // 队列里有排队的消息：自动继续发送
    if (sendQueue.value.length) window.setTimeout(drainQueue, 120)
  }
}

function handleStop() {
  abortedByUser = true
  clearApproval()
  controller?.abort()
}

/** 取消一条排队中的消息 */
function cancelQueued(i: number) {
  sendQueue.value.splice(i, 1)
}

/** 回退会话到第 index 条用户消息重来（文件随之恢复） */
async function handleRevert(index: number | undefined) {
  if (index === undefined || !currentId.value) return
  if (streaming.value) return
  const n = index + 1
  const ok = await askConfirm({
    title: '回到这条消息重来',
    message: `将删除这条消息之后的所有对话（保留前 ${n} 条），并把它之后小柯改动过的文件恢复原状。确定继续？`,
    confirmText: '回退',
  })
  if (!ok) return
  try {
    const r = await api.revertSession(currentId.value, index)
    const s = await api.getSession(currentId.value)
    messages.value = s.messages
    sessionTodos.value = s.todos || []
    const touched = r.files?.length
      ? '；恢复文件：' + r.files.map((f) => `${f.path}(${f.action === 'deleted' ? '已删除' : f.action === 'restored' ? '已恢复' : '跳过'})`).join('、')
      : ''
    error.value = ''
    compactNotice.value = `已回退到第 ${n} 条消息${touched}`
    if (compactTimer) window.clearTimeout(compactTimer)
    compactTimer = window.setTimeout(() => (compactNotice.value = ''), 6000)
    refreshSessions()
  } catch (e) {
    error.value = (e as Error).message
  }
}

/** 分叉会话：复制全部消息为独立副本继续聊 */
async function handleFork(id: string) {
  try {
    const r = await api.forkSession(id)
    await refreshSessions()
    await handleSelect(r.id)
  } catch (e) {
    error.value = (e as Error).message
  }
}

function handleNewChat() {
  if (streaming.value) handleStop()
  clearApproval()
  clearSendQueue()
  currentId.value = ''
  rememberSession('')
  messages.value = []
  sessionTodos.value = []
  lastUsage.value = null
  liveTools.value = []
  error.value = ''
}

async function handleSelect(id: string) {
  if (id === currentId.value) return
  if (streaming.value) handleStop()
  clearApproval()
  clearSendQueue()
  try {
    const s = await api.getSession(id)
    currentId.value = s.id
    rememberSession(s.id)
    currentProjectId.value = s.projectId || ''
    if (s.agentId) currentAgentId.value = s.agentId
    messages.value = s.messages
    sessionTodos.value = s.todos || []
    lastUsage.value = null
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function handleRename(id: string, title: string) {
  try {
    await api.renameSession(id, title)
    await refreshSessions()
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function handleRemove(id: string) {
  const s = sessions.value.find((x) => x.id === id)
  const ok = await askConfirm({
    title: `删除会话「${s?.title || '新对话'}」`,
    message: '会话会保留在「设置 → 高级 → 回收站」30 天，期间可随时恢复。',
    confirmText: '删除',
  })
  if (!ok) return
  try {
    await api.deleteSession(id)
  } catch {
    /* 删除失败时下次刷新还能看到 */
  }
  if (id === currentId.value) {
    currentId.value = ''
    messages.value = []
    sessionTodos.value = []
    lastUsage.value = null
    try {
      localStorage.removeItem(CURRENT_SESSION_KEY)
    } catch {
      /* 忽略 */
    }
  }
  refreshSessions()
}

// 选择项目 = 在该项目下开一个空白对话
function handleSelectProject(id: string) {
  if (currentProjectId.value === id && !currentId.value) return
  if (streaming.value) handleStop()
  clearSendQueue()
  currentProjectId.value = id
  currentId.value = ''
  messages.value = []
  sessionTodos.value = []
  lastUsage.value = null
  error.value = ''
}

// 解绑项目，回到通用对话
function unbindProject() {
  if (streaming.value) handleStop()
  clearSendQueue()
  currentProjectId.value = ''
  currentId.value = ''
  messages.value = []
  sessionTodos.value = []
  lastUsage.value = null
  error.value = ''
}

function handleProjectCreated(p: Project) {
  projects.value = [p, ...projects.value]
  handleSelectProject(p.id)
}

async function handleDeleteProject(id: string) {
  const ok = await askConfirm({
    title: '删除项目',
    message: '会连带删除项目目录里的全部文件，且无法从回收站恢复。确定继续？',
    confirmText: '删除',
  })
  if (!ok) return
  try {
    await api.deleteProject(id)
  } catch {
    /* 忽略 */
  }
  await refreshProjects()
}

function onSettingsClosed() {
  refreshHealth()
  refreshAgents()
}
</script>

<template>
  <div class="app">
    <SideBar
      :sessions="sessions"
      :current-id="currentId"
      :current-project-id="currentProjectId"
      :projects="projects"
      :agents="agents"
      :current-agent-id="currentAgentId"
      :mode="mode"
      :model="model"
      @select="handleSelect"
      @new-chat="handleNewChat"
      @rename="handleRename"
      @remove="handleRemove"
      @select-project="handleSelectProject"
      @new-project="projectModalOpen = true"
      @remove-project="handleDeleteProject"
      @select-agent="handleSelectAgent"
      @open-settings="settingsOpen = true"
      @fork="handleFork"
    />
    <main class="main">
      <div v-if="currentProject" class="proj-banner">
        <div class="pb-left">
          <Icon name="folder" :size="14" />
          <span class="pb-name">{{ currentProject.name }}</span>
        </div>
        <span class="pb-path" :title="currentProject.path">{{ currentProject.path }}</span>
        <button class="pb-x" title="退出项目（回到通用对话）" @click="unbindProject">
          <Icon name="x" :size="13" :stroke="2" />
        </button>
      </div>
      <transition name="toast">
        <div v-if="compactNotice" class="compact-notice">{{ compactNotice }}</div>
      </transition>
      <transition name="toast">
        <div v-if="retryNotice" class="compact-notice retry-notice">{{ retryNotice }}</div>
      </transition>
      <!-- 排队中的消息（当前轮结束后自动逐条发送，点击移除） -->
      <div v-if="sendQueue.length" class="queue-strip">
        <span v-for="(q, i) in sendQueue" :key="i" class="queue-chip" :title="q.content + '\n（点击移除）'" @click="cancelQueued(i)">
          <Icon name="queue" :size="12" />
          <span class="qc-text">{{ q.content.slice(0, 40) }}</span>
          <Icon name="x" :size="11" :stroke="2" />
        </span>
      </div>
      <ChatView
        :turns="turns"
        :streaming="streaming"
        :streaming-text="streamingText"
        :live-tools="liveTools"
        :error="error"
        :greeting="greeting"
        :empty-project-hint="emptyProjectHint"
        :current-project="currentProject"
        :agent="currentAgent"
        :messages="messages"
        :awaiting-approval="planAwaitingApproval"
        :todos="sessionTodos"
        :last-usage="lastUsage"
        @approve="handleApprove"
        @approve-custom="handleApproveCustom"
        @adjust-plan="handleAdjustPlan"
        @quick="handleSend"
        @revert="handleRevert"
      />
      <Composer
        :busy="streaming"
        :reasoning="reasoning"
        :mode="workMode"
        :session-id="currentId"
        :commands="slashCommands"
        @send="handleSend"
        @stop="handleStop"
        @update:reasoning="setReasoning"
        @set-mode="onModeChange"
      />
    </main>

    <SettingsView v-if="settingsOpen" @close="settingsOpen = false; onSettingsClosed()" />
    <ProjectModal
      v-if="projectModalOpen"
      @close="projectModalOpen = false"
      @created="handleProjectCreated"
    />
    <ConfirmDialog />
    <AskDialog :request="askRequest" @answer="handleAskAnswer" />
  </div>
</template>

<style scoped>
.app {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: transparent;
}

.proj-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 26px;
  background: rgba(10, 11, 15, 0.5);
  border-bottom: 1px solid var(--border);
  backdrop-filter: blur(16px);
  font-size: 12px;
}

.pb-left {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--accent);
  flex-shrink: 0;
}

.pb-name {
  color: var(--text);
  font-weight: 600;
  letter-spacing: 0.01em;
}

.pb-path {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pb-x {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid transparent;
  background: none;
  color: var(--text-faint);
  border-radius: 7px;
  flex-shrink: 0;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.pb-x:hover {
  color: var(--danger);
  border-color: rgba(251, 113, 133, 0.25);
  background: var(--danger-soft);
}


.compact-notice {
  position: fixed;
  top: 78px;
  left: 50%;
  transform: translateX(-50%);
  max-width: min(560px, calc(100vw - 48px));
  background: rgba(96, 165, 250, 0.12);
  border: 1px solid rgba(96, 165, 250, 0.3);
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
  padding: 10px 18px;
  border-radius: 999px;
  box-shadow: var(--shadow);
  backdrop-filter: blur(18px);
  z-index: 60;
  pointer-events: none;
}

.retry-notice {
  top: 122px;
  background: rgba(250, 204, 21, 0.1);
  border-color: rgba(250, 204, 21, 0.35);
}

.queue-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-width: var(--content-w);
  margin: 0 auto;
  padding: 0 28px 6px;
  width: 100%;
}

.queue-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 320px;
  padding: 4px 10px;
  border: 1px dashed rgba(255, 154, 61, 0.4);
  border-radius: 999px;
  background: rgba(255, 154, 61, 0.07);
  color: var(--text-muted);
  font-size: 11.5px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.queue-chip:hover {
  border-color: rgba(251, 113, 133, 0.5);
  background: rgba(251, 113, 133, 0.08);
}

.qc-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 720px) {
  .app {
    flex-direction: column;
  }

  .proj-banner {
    padding: 9px 16px;
  }
}
</style>
