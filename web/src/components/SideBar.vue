<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Agent, LLMProfileView, Project, SessionMeta } from '../types'
import { api } from '../lib/api'
import AvatarBadge from './AvatarBadge.vue'
import Icon from './Icon.vue'

const props = defineProps<{
  sessions: SessionMeta[]
  currentId: string
  currentProjectId: string
  projects: Project[]
  agents: Agent[]
  currentAgentId: string
  mode: string
  model: string
  /** 模型配置档案：底部快速切换菜单用 */
  profiles?: LLMProfileView[]
  activeProfile?: string
}>()

// 模型快速切换菜单（foot 区弹出）
const modelMenuOpen = ref(false)
const modelMenuWrap = ref<HTMLElement | null>(null)
function onDocClick(e: MouseEvent) {
  if (!modelMenuOpen.value) return
  if (modelMenuWrap.value && !modelMenuWrap.value.contains(e.target as Node)) modelMenuOpen.value = false
}
onMounted(() => document.addEventListener('mousedown', onDocClick))
onUnmounted(() => document.removeEventListener('mousedown', onDocClick))
function pickModel(id: string) {
  modelMenuOpen.value = false
  if (id !== props.activeProfile) emit('activateModel', id)
}

const emit = defineEmits<{
  select: [id: string]
  newChat: []
  rename: [id: string, title: string]
  remove: [id: string]
  selectProject: [id: string]
  newProject: []
  removeProject: [id: string]
  selectAgent: [id: string]
  openSettings: []
  /** 分叉会话：复制全部消息为新会话 */
  fork: [id: string]
  /** 一键切换模型配置档案 */
  activateModel: [id: string]
}>()


// 会话重命名：双击标题进入行内编辑，Enter 确认 / Esc 取消
const renamingId = ref('')
const renamingText = ref('')
function startRename(s: SessionMeta) {
  renamingId.value = s.id
  renamingText.value = s.title || ''
}
function commitRename() {
  const id = renamingId.value
  const title = renamingText.value.trim()
  renamingId.value = ''
  if (!id) return
  emit('rename', id, title)
}
function cancelRename() {
  renamingId.value = ''
  renamingText.value = ''
}

// 会话搜索：标题/ID 本地过滤；输入 ≥2 字符按回车 → 后端全文搜索消息内容
//（跨会话找「哪次聊过」，点击命中跳转会话）
const sessionQuery = ref('')
const filteredSessions = computed(() => {
  const q = sessionQuery.value.trim().toLowerCase()
  if (!q) return props.sessions
  return props.sessions.filter(
    (s) => (s.title || '').toLowerCase().includes(q) || s.id.includes(q),
  )
})

interface ContentHit {
  sessionId: string
  title: string
  role: string
  snippet: string
}
const contentHits = ref<ContentHit[]>([])
const searchBusy = ref(false)
const searchErr = ref('')
async function searchContent() {
  const q = sessionQuery.value.trim()
  if (q.length < 2) return
  searchBusy.value = true
  searchErr.value = ''
  try {
    const r = await api.search(q)
    contentHits.value = r.hits
    if (!r.hits.length) searchErr.value = '没有会话内容命中。'
  } catch (e) {
    searchErr.value = (e as Error).message
  } finally {
    searchBusy.value = false
  }
}
function clearContentHits() {
  contentHits.value = []
  searchErr.value = ''
}
watch(sessionQuery, (v) => {
  if (!v.trim()) clearContentHits()
})
function jumpToHit(id: string) {
  clearContentHits()
  sessionQuery.value = ''
  emit('select', id)
}

// 智能体切换下拉
const expanding = ref(false)
const currentAgent = computed<Agent | null>(
  () => props.agents.find((a) => a.id === props.currentAgentId) || props.agents[0] || null,
)

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (sameDay) return hm
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
}

function projectName(projects: Project[], id?: string): string {
  if (!id) return ''
  return projects.find((p) => p.id === id)?.name || ''
}
</script>

<template>
  <aside class="sidebar">
    <!-- 智能体切换器 -->
    <div class="agent-switch" :class="{ open: expanding }" @click="expanding = !expanding">
      <div class="as-row">
        <AvatarBadge :value="currentAgent?.avatar" :fallback="currentAgent?.name || '小柯'" :size="40" />
        <div class="as-meta">
          <div class="as-name">{{ currentAgent?.name || '小柯' }}</div>
          <div class="as-sub">点击切换智能体</div>
        </div>
        <span class="as-caret"><Icon name="chevron-down" :size="14" /></span>
      </div>
      <div v-if="expanding" class="as-list">
        <div
          v-for="a in props.agents"
          :key="a.id"
          class="as-item"
          :class="{ active: a.id === currentAgentId }"
          @click.stop="emit('selectAgent', a.id); expanding = false"
        >
          <AvatarBadge :value="a.avatar" :fallback="a.name" :size="26" />
          <span class="as-item-name">{{ a.name }}</span>
          <span v-if="a.builtin" class="as-tag" title="内置人格，不可修改">内置</span>
          <span v-if="a.id === currentAgentId" class="as-check"><Icon name="check" :size="13" /></span>
        </div>
        <div v-if="props.agents.length <= 1" class="as-hint">可在 设置 → 智能体 里创建更多人格</div>
      </div>
      <span class="brand-status" title="在线"></span>
    </div>

    <button class="new-chat" @click="emit('newChat')">
      <Icon name="plus" :size="15" :stroke="2.4" />
      新对话
    </button>

    <div class="section-label">
      项目
      <button class="add-mini" title="新建项目" @click="emit('newProject')">
        <Icon name="plus" :size="12" :stroke="2.2" />
      </button>
    </div>
    <div class="list short">
      <div
        v-for="p in props.projects"
        :key="p.id"
        class="item"
        :class="{ active: p.id === currentProjectId }"
        :title="p.path"
        @click="emit('selectProject', p.id)"
      >
        <span class="folder"><Icon name="folder" :size="15" /></span>
        <span class="item-title">{{ p.name }}</span>
        <button class="item-del" title="删除项目（含目录内全部文件）" @click.stop="emit('removeProject', p.id)">
          <Icon name="x" :size="12" :stroke="2" />
        </button>
      </div>
      <div v-if="!projects.length" class="empty">还没有项目，点「＋」创建一个。</div>
    </div>

    <div class="section-label">
      最近会话
      <span class="sess-count">{{ filteredSessions.length }}</span>
    </div>
    <input
      v-model="sessionQuery"
      class="sess-search"
      type="search"
      placeholder="搜索会话…（回车搜内容）"
      @keydown.enter.prevent="searchContent"
    />
    <!-- 全文搜索结果（跨会话搜消息内容） -->
    <div v-if="contentHits.length || searchErr" class="search-drop">
      <div v-if="searchErr" class="empty">{{ searchErr }}</div>
      <button v-for="(h, hi) in contentHits" :key="hi" class="hit-item" :title="h.snippet" @click="jumpToHit(h.sessionId)">
        <span class="hit-title">{{ h.title || '新对话' }}</span>
        <span class="hit-snippet">{{ h.role === 'user' ? '🧑' : '🤖' }} {{ h.snippet }}</span>
      </button>
    </div>
    <div class="list">
      <div
        v-for="s in filteredSessions"
        :key="s.id"
        class="item"
        :class="{ active: s.id === currentId }"
        @click="emit('select', s.id)"
      >
        <span v-if="projectName(projects, s.projectId)" class="proj-badge" :title="'项目：' + projectName(projects, s.projectId)">
          {{ projectName(projects, s.projectId).slice(0, 4) }}
        </span>
        <input
          v-if="renamingId === s.id"
          v-model="renamingText"
          class="item-rename"
          autofocus
          @click.stop
          @keydown.enter.prevent="commitRename"
          @keydown.esc.prevent="cancelRename"
          @blur="commitRename"
        />
        <span
          v-else
          class="item-title"
          :title="s.title || '新对话（双击重命名）'"
          @dblclick.stop="startRename(s)"
        >{{ s.title || '新对话' }}</span>
        <span class="item-time">{{ fmtTime(s.updatedAt) }}</span>
        <button class="item-export" title="分叉会话：复制全部消息为独立副本" @click.stop="emit('fork', s.id)">
          <Icon name="clipboard" :size="12" />
        </button>
        <a
          class="item-export"
          :href="`/api/sessions/${s.id}/export`"
          :download="`session-${s.id}.json`"
          title="导出这个会话（JSON）"
          @click.stop
        >
          <Icon name="download" :size="12" />
        </a>
        <a
          class="item-export"
          :href="`/api/sessions/${s.id}/export?format=md`"
          :download="`session-${s.id}.md`"
          title="导出这个会话（Markdown）"
          @click.stop
        >
          <Icon name="book" :size="12" />
        </a>
        <button class="item-del" title="删除会话" @click.stop="emit('remove', s.id)">
          <Icon name="x" :size="12" :stroke="2" />
        </button>
      </div>
      <div v-if="!filteredSessions.length && sessionQuery.trim()" class="empty">没有匹配「{{ sessionQuery.trim() }}」的会话。</div>
      <div v-else-if="!sessions.length" class="empty">还没有会话。</div>
    </div>

    <div ref="modelMenuWrap" class="foot">
      <button
        class="foot-btn"
        :title="(props.profiles?.length || 0) > 1 ? '点击切换模型' : '当前模型'"
        @click="modelMenuOpen = !modelMenuOpen"
      >
        <span class="mode-dot" :class="mode === 'mock' ? 'amber' : 'green'"></span>
        <span class="foot-text">{{ mode === 'mock' ? '演示模式' : '在线模型' }} · {{ model || '—' }}</span>
      </button>
      <!-- 模型档案快速切换 -->
      <div v-if="modelMenuOpen" class="model-menu">
        <button
          v-for="p in props.profiles || []"
          :key="p.id"
          class="mm-opt"
          :class="{ active: p.id === props.activeProfile }"
          @click="pickModel(p.id)"
        >
          <span class="mm-main">
            <b>{{ p.name }}</b>
            <i>{{ p.model || '（未设模型）' }}</i>
          </span>
          <span v-if="p.id === props.activeProfile" class="mm-check"><Icon name="check" :size="13" /></span>
        </button>
        <button class="mm-manage" @click="modelMenuOpen = false; emit('openSettings')">
          <Icon name="settings" :size="12" /> 管理配置…
        </button>
      </div>
      <button class="gear" title="设置" @click="emit('openSettings')">
        <Icon name="settings" :size="16" />
      </button>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  width: var(--sidebar-w);
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 14px 12px;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.035), transparent 26%),
    rgba(9, 10, 14, 0.72);
  border-right: 1px solid var(--border);
  backdrop-filter: blur(22px);
  position: relative;
}

.sidebar::after {
  content: '';
  position: absolute;
  top: 0;
  right: -1px;
  width: 1px;
  height: 180px;
  background: linear-gradient(180deg, rgba(255, 154, 61, 0.35), transparent);
  opacity: 0.55;
}



.item-rename {
  flex: 1;
  min-width: 0;
  border: 1px solid var(--border-accent);
  background: var(--bg-inset);
  border-radius: 6px;
  padding: 3px 8px;
  font-size: 13px;
  color: var(--text);
  outline: none;
}

/* ---------- 全文搜索下拉 ---------- */
.search-drop {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 4px 0 8px;
  padding: 5px;
  border: 1px solid var(--border-strong);
  border-radius: 12px;
  background: linear-gradient(180deg, #1c202a 0%, #14171f 100%);
  box-shadow: var(--shadow);
  max-height: 300px;
  overflow-y: auto;
}

.hit-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 7px 9px;
  border: none;
  border-radius: 8px;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s ease;
}

.hit-item:hover {
  background: rgba(255, 154, 61, 0.09);
}

.hit-title {
  font-size: 12px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hit-snippet {
  font-size: 11px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sess-search {
  border: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.03);
  border-radius: 8px;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--text);
  outline: none;
  transition: border-color var(--dur) var(--ease);
}

.sess-search:focus {
  border-color: var(--border-accent);
}

.sess-search::placeholder {
  color: var(--text-faint);
}

.sess-count {
  margin-left: auto;
  font-size: 10px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

/* ---------- 智能体切换器 ---------- */
.agent-switch {
  position: relative;
  background: linear-gradient(150deg, rgba(32, 37, 48, 0.96), rgba(17, 20, 27, 0.98));
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  cursor: pointer;
  box-shadow: var(--shadow-sm), var(--ring-inset);
  transition:
    border-color var(--dur) var(--ease),
    transform var(--dur) var(--ease),
    box-shadow var(--dur) var(--ease);
}

.agent-switch:hover {
  border-color: var(--border-strong);
  transform: translateY(-1px);
}

.agent-switch.open {
  border-color: var(--border-accent);
  box-shadow: var(--shadow-soft), 0 0 0 3px rgba(255, 154, 61, 0.08);
}

.as-row {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 12px;
}

.as-meta {
  flex: 1;
  min-width: 0;
}

.as-name {
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.01em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.as-sub {
  margin-top: 1px;
  font-size: 10.5px;
  color: var(--text-faint);
}

.as-caret {
  display: flex;
  color: var(--text-faint);
  transition: transform var(--dur) var(--ease), color var(--dur) var(--ease);
}

.agent-switch.open .as-caret {
  transform: rotate(180deg);
  color: var(--accent);
}

.as-list {
  border-top: 1px solid var(--border);
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 240px;
  overflow-y: auto;
}

.as-item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 9px;
  border-radius: 10px;
  transition: background 0.16s ease;
}

.as-item:hover {
  background: rgba(255, 255, 255, 0.05);
}

.as-item.active {
  background: var(--accent-soft);
}

.as-item-name {
  flex: 1;
  font-size: 13px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.as-check {
  display: flex;
  color: var(--accent);
}

.as-tag {
  flex-shrink: 0;
  padding: 2px 6px;
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--text-faint);
  font-size: 9.5px;
  letter-spacing: 0.04em;
}

.as-hint {
  padding: 8px 9px 4px;
  color: var(--text-faint);
  font-size: 11px;
  line-height: 1.5;
}

.brand-status {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 0 4px rgba(74, 222, 128, 0.08), 0 0 12px rgba(74, 222, 128, 0.65);
  position: absolute;
  top: 14px;
  right: 14px;
}

.new-chat {
  position: relative;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 11px 14px;
  background: var(--accent-gradient);
  color: var(--accent-text);
  border: none;
  border-radius: var(--radius);
  font-size: 13.5px;
  font-weight: 700;
  box-shadow: var(--shadow-accent), inset 0 1px 0 rgba(255, 255, 255, 0.35);
  transition: transform var(--dur) var(--ease), filter var(--dur) var(--ease);
}

.new-chat::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(100deg, transparent 20%, rgba(255, 255, 255, 0.3) 50%, transparent 80%);
  transform: translateX(-130%);
  transition: transform 0.6s var(--ease);
}

.new-chat:hover {
  transform: translateY(-1px);
  filter: brightness(1.04);
}

.new-chat:hover::after {
  transform: translateX(130%);
}

.section-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 8px;
  margin-top: 4px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--text-faint);
}

.add-mini {
  width: 20px;
  height: 20px;
  border: 1px solid transparent;
  background: rgba(255, 255, 255, 0.04);
  color: var(--text-faint);
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.add-mini:hover {
  color: var(--accent);
  border-color: var(--border);
  background: var(--accent-soft);
}

.list {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-right: 2px;
}

.list.short {
  flex: 0 0 auto;
  max-height: 170px;
}

.item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 10px;
  border-radius: 10px;
  cursor: pointer;
  color: var(--text-muted);
  border: 1px solid transparent;
  transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
}

.item:hover {
  background: rgba(255, 255, 255, 0.045);
  color: var(--text);
}

.item.active {
  background: linear-gradient(90deg, var(--accent-soft), rgba(255, 154, 61, 0.025));
  border-color: rgba(255, 154, 61, 0.16);
  color: var(--text);
  box-shadow: inset 3px 0 0 var(--accent);
}

.folder {
  display: flex;
  color: var(--accent);
  opacity: 0.9;
}

.proj-badge {
  flex-shrink: 0;
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.04em;
  color: var(--accent);
  background: rgba(255, 154, 61, 0.1);
  border: 1px solid rgba(255, 154, 61, 0.16);
  border-radius: 5px;
  padding: 1px 5px;
  max-width: 54px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
}

.item-time {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.item-export {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  color: var(--text-faint);
  border-radius: 5px;
  text-decoration: none;
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.15s ease, visibility 0.15s ease, color 0.15s ease, background 0.15s ease;
}

.item:hover .item-export {
  opacity: 1;
  visibility: visible;
}

.item-export:hover {
  color: var(--accent);
  background: rgba(255, 154, 61, 0.12);
}

.item-del {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  background: none;
  color: var(--text-faint);
  border-radius: 5px;
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.15s ease, visibility 0.15s ease, color 0.15s ease, background 0.15s ease;
}

.item:hover .item-del {
  opacity: 1;
  visibility: visible;
}

.item-del:hover {
  color: var(--danger);
  background: var(--danger-soft);
}

.empty {
  padding: 9px 8px;
  color: var(--text-faint);
  font-size: 11.5px;
  line-height: 1.6;
}

/* ---------- 底部模型快速切换 ---------- */
.foot-btn {
  display: flex;
  align-items: center;
  gap: 7px;
  flex: 1;
  min-width: 0;
  padding: 4px 6px;
  border: none;
  border-radius: 9px;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.foot-btn:hover {
  background: rgba(255, 255, 255, 0.06);
}

.model-menu {
  position: absolute;
  bottom: calc(100% + 10px);
  left: 10px;
  right: 10px;
  background: linear-gradient(180deg, #1c202a 0%, #14171f 100%);
  border: 1px solid var(--border-strong);
  border-radius: 14px;
  box-shadow: var(--shadow);
  padding: 5px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  z-index: 40;
}

.mm-opt {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: none;
  border-radius: 9px;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s ease;
}

.mm-opt:hover {
  background: rgba(255, 154, 61, 0.09);
}

.mm-opt.active {
  background: linear-gradient(135deg, rgba(255, 154, 61, 0.14), rgba(255, 154, 61, 0.05));
}

.mm-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.mm-main b {
  font-size: 12px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mm-main i {
  font-style: normal;
  font-size: 10.5px;
  color: var(--text-faint);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mm-check {
  display: flex;
  color: var(--accent);
}

.mm-manage {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 3px;
  padding: 7px 10px;
  border: none;
  border-top: 1px solid var(--border);
  border-radius: 0 0 10px 10px;
  background: none;
  color: var(--text-muted);
  font-size: 11.5px;
  cursor: pointer;
  transition: color 0.12s ease, background 0.12s ease;
}

.mm-manage:hover {
  color: var(--accent);
  background: rgba(255, 255, 255, 0.04);
}

.foot {
  position: relative; /* 模型切换菜单的定位锚点 */
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 11px 6px 2px;
  border-top: 1px solid var(--border);
}

.gear {
  border: 1px solid transparent;
  background: none;
  color: var(--text-faint);
  width: 30px;
  height: 30px;
  padding: 0;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-left: auto;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.gear:hover {
  color: var(--accent);
  border-color: var(--border);
  background: rgba(255, 255, 255, 0.04);
}

.mode-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.mode-dot.green {
  background: var(--ok);
  box-shadow: 0 0 8px rgba(74, 222, 128, 0.55);
}

.mode-dot.amber {
  background: var(--warn);
  box-shadow: 0 0 8px rgba(251, 191, 36, 0.45);
}

.foot-text {
  font-size: 10.5px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
