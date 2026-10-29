<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Agent, Project, SessionMeta } from '../types'
import AvatarBadge from './AvatarBadge.vue'

const props = defineProps<{
  sessions: SessionMeta[]
  currentId: string
  currentProjectId: string
  projects: Project[]
  agents: Agent[]
  currentAgentId: string
  mode: string
  model: string
}>()

const emit = defineEmits<{
  select: [id: string]
  newChat: []
  remove: [id: string]
  selectProject: [id: string]
  newProject: []
  removeProject: [id: string]
  selectAgent: [id: string]
  openSettings: []
}>()

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
        <span class="as-caret">▾</span>
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
          <span v-if="a.id === currentAgentId" class="as-check">✓</span>
        </div>
      </div>
      <span class="brand-status" title="在线"></span>
    </div>

    <button class="new-chat" @click="emit('newChat')">
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round">
        <path d="M12 5v14M5 12h14" />
      </svg>
      新对话
    </button>

    <div class="section-label">
      项目
      <button class="add-mini" title="新建项目" @click="emit('newProject')">＋</button>
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
        <span class="folder">📁</span>
        <span class="item-title">{{ p.name }}</span>
        <button class="item-del" title="删除项目（含目录内全部文件）" @click.stop="emit('removeProject', p.id)">
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
      <div v-if="!projects.length" class="empty">还没有项目，点「＋」创建一个。</div>
    </div>

    <div class="section-label">最近会话</div>
    <div class="list">
      <div
        v-for="s in sessions"
        :key="s.id"
        class="item"
        :class="{ active: s.id === currentId }"
        @click="emit('select', s.id)"
      >
        <span v-if="projectName(projects, s.projectId)" class="proj-badge" :title="'项目：' + projectName(projects, s.projectId)">
          {{ projectName(projects, s.projectId).slice(0, 4) }}
        </span>
        <span class="item-title">{{ s.title || '新对话' }}</span>
        <span class="item-time">{{ fmtTime(s.updatedAt) }}</span>
        <button class="item-del" title="删除会话" @click.stop="emit('remove', s.id)">
          <svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
      <div v-if="!sessions.length" class="empty">还没有会话。</div>
    </div>

    <div class="foot">
      <span class="mode-dot" :class="mode === 'mock' ? 'amber' : 'green'"></span>
      <span class="foot-text">{{ mode === 'mock' ? '演示模式' : '在线模型' }} · {{ model || '—' }}</span>
      <button class="gear" title="设置" @click="emit('openSettings')">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </button>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  width: var(--sidebar-w);
  flex-shrink: 0;
  background: var(--bg-surface);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  padding: 14px 12px;
  gap: 12px;
}

/* ---------- 智能体切换器 ---------- */
.agent-switch {
  position: relative;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: border-color 0.15s ease;
}

.agent-switch:hover {
  border-color: var(--border-strong);
}

.agent-switch.open {
  border-color: var(--accent);
}

.as-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 11px;
}

.as-meta {
  flex: 1;
  min-width: 0;
}

.as-name {
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.as-sub {
  font-size: 11px;
  color: var(--text-faint);
}

.as-caret {
  color: var(--text-faint);
  font-size: 11px;
  transition: transform 0.15s ease;
}

.agent-switch.open .as-caret {
  transform: rotate(180deg);
}

.as-list {
  border-top: 1px solid var(--border);
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 220px;
  overflow-y: auto;
}

.as-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 9px;
  border-radius: var(--radius-sm);
  transition: background 0.12s ease;
}

.as-item:hover {
  background: var(--bg-surface-2);
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
  color: var(--accent);
  font-size: 12px;
}

.brand-status {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 6px var(--ok);
  position: absolute;
  top: 12px;
  right: 11px;
}

.new-chat {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 9px 12px;
  background: var(--accent);
  color: var(--accent-text);
  border: none;
  border-radius: var(--radius);
  font-weight: 600;
  transition: background 0.15s ease;
}

.new-chat:hover {
  background: var(--accent-hover);
}

.section-label {
  font-size: 11px;
  color: var(--text-faint);
  letter-spacing: 1px;
  padding: 0 6px;
  margin-top: 4px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.add-mini {
  border: 1px solid var(--border);
  background: none;
  color: var(--text-muted);
  border-radius: 4px;
  width: 18px;
  height: 18px;
  line-height: 1;
  font-size: 12px;
}

.add-mini:hover {
  color: var(--accent);
  border-color: var(--accent);
}

.list {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.list.short {
  flex: 0 0 auto;
  max-height: 168px;
}

.item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  color: var(--text-muted);
  border: 1px solid transparent;
  transition: background 0.12s ease, color 0.12s ease;
}

.item:hover {
  background: var(--bg-surface-2);
  color: var(--text);
}

.item.active {
  background: var(--accent-soft);
  border-color: var(--border);
  color: var(--text);
}

.folder {
  font-size: 13px;
}

.proj-badge {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 4px;
  padding: 1px 5px;
  max-width: 52px;
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
  font-size: 13px;
}

.item-time {
  font-size: 11px;
  color: var(--text-faint);
  flex-shrink: 0;
}

.item-del {
  flex-shrink: 0;
  display: none;
  border: none;
  background: none;
  color: var(--text-faint);
  padding: 2px;
  border-radius: 4px;
}

.item:hover .item-del {
  display: flex;
}

.item-del:hover {
  color: var(--danger);
  background: rgba(255, 107, 107, 0.12);
}

.empty {
  padding: 8px 6px;
  color: var(--text-faint);
  font-size: 12px;
}

.foot {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 6px 2px;
  border-top: 1px solid var(--border);
}

.gear {
  border: none;
  background: none;
  color: var(--text-faint);
  padding: 5px;
  border-radius: var(--radius-sm);
  display: flex;
  margin-left: auto;
  cursor: pointer;
}

.gear:hover {
  color: var(--accent);
  background: var(--bg-surface-2);
}

.mode-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.mode-dot.green {
  background: var(--ok);
}

.mode-dot.amber {
  background: var(--warn);
}

.foot-text {
  font-size: 11px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
