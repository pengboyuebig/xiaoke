<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from './lib/api'
import type { Agent, Message, MsgGroup, Project, SessionMeta, Skill, ToolActivity, ToolPair, Turn } from './types'
import SideBar from './components/SideBar.vue'
import ChatView from './components/ChatView.vue'
import Composer from './components/Composer.vue'
import SettingsView from './components/SettingsView.vue'
import ProjectModal from './components/ProjectModal.vue'
import Icon from './components/Icon.vue'

const sessions = ref<SessionMeta[]>([])
const currentId = ref('')
const messages = ref<Message[]>([])
const greeting = ref('**小柯** 你好，我是小柯。有什么想问的，尽管说。')
const emptyProjectHint = ref('')
const streaming = ref(false)
const streamingText = ref('')
const liveTools = ref<ToolActivity[]>([])
const error = ref('')
const mode = ref('')
const model = ref('')

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
type WorkMode = 'plan' | 'build'
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

/** 批准计划：切开发模式 → 自动发送「开始」按计划开工（该消息不显示在对话里） */
function handleApprove() {
  if (streaming.value) return
  clearApproval()
  setWorkMode('build')
  handleSend('开始', { hidden: true })
}

/** 批准但用自己的实现方式：切开发模式 → 发送用户写的指令（正常显示） */
function handleApproveCustom(text: string) {
  const t = text.trim()
  if (!t || streaming.value) return
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
  currentAgentId.value = id
  currentId.value = ''
  rememberSession('')
  messages.value = []
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

  for (const m of messages.value) {
    if (m.role === 'user') {
      flushRound()
      // 隐藏消息（如批准计划的自动指令）不显示问题气泡，但仍开启一个新回合承接回复
      cur = { question: m.hidden ? '' : m.content, groups: [], startTime: m.createdAt }
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
  if (streaming.value) return
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
      onToolStart: (name, args) => {
        liveTools.value.push({ name, args, done: false })
      },
      onToolResult: (info) => {
        // 找到最近一个同名未完成的活动行
        for (let i = liveTools.value.length - 1; i >= 0; i--) {
          const t = liveTools.value[i]
          if (t.name === info.name && !t.done) {
            t.done = true
            t.ok = info.ok
            t.preview = info.preview
            t.error = info.error
            t.ms = info.ms
            return
          }
        }
      },
    })
    // 无论正常结束还是中途停止，都以服务端落库为准刷新一次
    let serverHadThis = false
    if (r.sessionId === currentId.value || !currentId.value) {
      currentId.value = r.sessionId
      rememberSession(r.sessionId)
      try {
        const s = await api.getSession(r.sessionId)
        if (r.sessionId === currentId.value) {
          messages.value = s.messages
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
    // 中途停止且服务端没保存这条内容：本地保留半截并标记「已停止」
    if (abortedByUser && !serverHadThis && r.sessionId === currentId.value && streamingText.value) {
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
    streaming.value = false
    streamingText.value = ''
    liveTools.value = []
    controller = null
    // 无论成功/停止/出错都刷新会话列表：会话已落库，必须出现在侧栏（持久化可见）
    refreshSessions()
  }
}

function handleStop() {
  abortedByUser = true
  clearApproval()
  controller?.abort()
}

function handleNewChat() {
  if (streaming.value) handleStop()
  clearApproval()
  currentId.value = ''
  rememberSession('')
  messages.value = []
  liveTools.value = []
  error.value = ''
}

async function handleSelect(id: string) {
  if (id === currentId.value) return
  if (streaming.value) handleStop()
  clearApproval()
  try {
    const s = await api.getSession(id)
    currentId.value = s.id
    rememberSession(s.id)
    currentProjectId.value = s.projectId || ''
    if (s.agentId) currentAgentId.value = s.agentId
    messages.value = s.messages
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function handleRemove(id: string) {
  const s = sessions.value.find((x) => x.id === id)
  if (!confirm(`删除会话「${s?.title || '新对话'}」？删除后不可恢复。`)) return
  try {
    await api.deleteSession(id)
  } catch {
    /* 删除失败时下次刷新还能看到 */
  }
  if (id === currentId.value) {
    currentId.value = ''
    messages.value = []
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
  currentProjectId.value = id
  currentId.value = ''
  messages.value = []
  error.value = ''
}

// 解绑项目，回到通用对话
function unbindProject() {
  if (streaming.value) handleStop()
  currentProjectId.value = ''
  currentId.value = ''
  messages.value = []
  error.value = ''
}

function handleProjectCreated(p: Project) {
  projects.value = [p, ...projects.value]
  handleSelectProject(p.id)
}

async function handleDeleteProject(id: string) {
  if (!confirm('删除项目会连带删除项目目录里的全部文件，确定？')) return
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
      @remove="handleRemove"
      @select-project="handleSelectProject"
      @new-project="projectModalOpen = true"
      @remove-project="handleDeleteProject"
      @select-agent="handleSelectAgent"
      @open-settings="settingsOpen = true"
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
        @approve="handleApprove"
        @approve-custom="handleApproveCustom"
        @adjust-plan="handleAdjustPlan"
        @quick="handleSend"
      />
      <Composer
        :busy="streaming"
        :reasoning="reasoning"
        :mode="workMode"
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

@media (max-width: 720px) {
  .app {
    flex-direction: column;
  }

  .proj-banner {
    padding: 9px 16px;
  }
}
</style>
