<script setup lang="ts">
import { ref } from 'vue'
import Icon from './Icon.vue'
import type { SessionMeta } from '../types'

const props = defineProps<{
  session: SessionMeta
  active: boolean
  /** 嵌套在项目下（缩进显示，不显示项目徽标） */
  nested?: boolean
}>()

const emit = defineEmits<{
  select: [id: string]
  rename: [id: string, title: string]
  remove: [id: string]
  fork: [id: string]
}>()

// 重命名：双击标题进入行内编辑，Enter 确认 / Esc 取消
const renaming = ref(false)
const text = ref('')

function startRename() {
  renaming.value = true
  text.value = props.session.title || ''
}
function commitRename() {
  if (!renaming.value) return
  renaming.value = false
  emit('rename', props.session.id, text.value.trim())
}
function cancelRename() {
  renaming.value = false
  text.value = ''
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (sameDay) return hm
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
}
</script>

<template>
  <div
    class="item sess-item"
    :class="{ active, nested }"
    @click="emit('select', session.id)"
  >
    <input
      v-if="renaming"
      v-model="text"
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
      :title="session.title || '新对话（双击重命名）'"
      @dblclick.stop="startRename"
    >{{ session.title || '新对话' }}</span>
    <span class="item-time">{{ fmtTime(session.updatedAt) }}</span>
    <button class="item-export" title="分叉会话：复制全部消息为独立副本" @click.stop="emit('fork', session.id)">
      <Icon name="clipboard" :size="12" />
    </button>
    <a
      class="item-export"
      :href="`/api/sessions/${session.id}/export`"
      :download="`session-${session.id}.json`"
      title="导出这个会话（JSON）"
      @click.stop
    >
      <Icon name="download" :size="12" />
    </a>
    <a
      class="item-export"
      :href="`/api/sessions/${session.id}/export?format=md`"
      :download="`session-${session.id}.md`"
      title="导出这个会话（Markdown）"
      @click.stop
    >
      <Icon name="book" :size="12" />
    </a>
    <button class="item-del" title="删除会话" @click.stop="emit('remove', session.id)">
      <Icon name="x" :size="12" :stroke="2" />
    </button>
  </div>
</template>

<style scoped>
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

/* 嵌套在项目下：更紧凑、稍小字号，视觉上从属于上方项目 */
.item.nested {
  padding: 7px 10px 7px 8px;
  border-radius: 8px;
}

.item.nested .item-title {
  font-size: 12px;
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
</style>
