<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from './lib/api'
import type { Agent, Message, Project, SessionMeta, Skill, ToolActivity } from './types'
import SideBar from './components/SideBar.vue'
import ChatView from './components/ChatView.vue'
import Composer from './components/Composer.vue'
import SettingsView from './components/SettingsView.vue'
import ProjectModal from './components/ProjectModal.vue'

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

onMounted(() => {
  refreshSessions()
  refreshProjects()
  refreshAgents()
  refreshHealth()
})

// 切换智能体 = 开新对话（当前会话已绑定旧智能体，不混用）
function handleSelectAgent(id: string) {
  if (currentAgentId.value === id && !currentId.value) return
  if (streaming.value) handleStop()
  currentAgentId.value = id
  currentId.value = ''
  messages.value = []
  error.value = ''
}

async function handleSend(content: string) {
  if (streaming.value) return
  error.value = ''
  messages.value.push({ role: 'user', content })
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
    }
    const r = await api.chat({
      sessionId,
      content,
      projectId: currentProjectId.value,
      agentId: currentAgent.value?.id || '',
      reasoning: reasoning.value,
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
    streamingText.value = ''
    liveTools.value = []
    refreshSessions()
  } catch (e) {
    error.value = (e as Error).message || '出错了，请稍后再试'
  } finally {
    streaming.value = false
    streamingText.value = ''
    liveTools.value = []
    controller = null
  }
}

function handleStop() {
  abortedByUser = true
  controller?.abort()
}

function handleNewChat() {
  if (streaming.value) handleStop()
  currentId.value = ''
  messages.value = []
  liveTools.value = []
  error.value = ''
}

async function handleSelect(id: string) {
  if (id === currentId.value) return
  if (streaming.value) handleStop()
  try {
    const s = await api.getSession(id)
    currentId.value = s.id
    currentProjectId.value = s.projectId || ''
    if (s.agentId) currentAgentId.value = s.agentId
    messages.value = s.messages
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function handleRemove(id: string) {
  try {
    await api.deleteSession(id)
  } catch {
    /* 删除失败时下次刷新还能看到 */
  }
  if (id === currentId.value) {
    currentId.value = ''
    messages.value = []
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
        <span class="pb-name">📁 {{ currentProject.name }}</span>
        <span class="pb-path" :title="currentProject.path">{{ currentProject.path }}</span>
        <button class="pb-x" title="退出项目（回到通用对话）" @click="unbindProject">✕</button>
      </div>
      <ChatView
        :messages="messages"
        :streaming="streaming"
        :streaming-text="streamingText"
        :live-tools="liveTools"
        :error="error"
        :greeting="greeting"
        :empty-project-hint="emptyProjectHint"
        :current-project="currentProject"
        :agent="currentAgent"
      />
      <Composer
        :busy="streaming"
        :reasoning="reasoning"
        @send="handleSend"
        @stop="handleStop"
        @update:reasoning="setReasoning"
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
  background: var(--bg);
}

.proj-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 20px;
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border);
  font-size: 12px;
}

.pb-name {
  color: var(--accent);
  font-weight: 600;
  flex-shrink: 0;
}

.pb-path {
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}

.pb-x {
  border: 1px solid var(--border);
  background: none;
  color: var(--text-faint);
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 11px;
  flex-shrink: 0;
}

.pb-x:hover {
  color: var(--danger);
  border-color: var(--danger);
}

@media (max-width: 720px) {
  .app {
    flex-direction: column;
  }
}
</style>
