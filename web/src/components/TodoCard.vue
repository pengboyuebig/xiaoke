<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import type { Todo } from '../types'

const props = defineProps<{
  todos: Todo[]
  /** 是否展开（由父组件控制的抽屉状态） */
  open: boolean
}>()

const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const statusLabel: Record<string, string> = {
  pending: '待开始',
  in_progress: '进行中',
  completed: '已完成',
}

const doneCount = computed(() => props.todos.filter((t) => t.status === 'completed').length)
const allDone = computed(() => props.todos.length > 0 && doneCount.value === props.todos.length)

function toggle() {
  emit('update:open', !props.open)
}
</script>

<template>
  <!-- 收起态：竖排标签条，点一下展开 -->
  <button v-if="!open" class="todo-tab" :class="{ done: allDone }" title="展开待办清单" @click="toggle">
    <Icon name="check" :size="13" class="tab-icon" />
    <span class="tab-text">待办</span>
    <span class="tab-count">{{ doneCount }}/{{ todos.length }}</span>
    <Icon name="chevron-down" :size="12" class="tab-chevron" />
  </button>

  <!-- 展开态：完整清单 -->
  <div v-else class="todo-card">
    <button class="todo-head" title="收起" @click="toggle">
      <Icon name="check" class="todo-icon" />
      <span>待办清单</span>
      <span class="todo-count">{{ doneCount }}/{{ todos.length }}</span>
      <Icon name="chevron-up" :size="13" class="head-chevron" />
    </button>
    <ul class="todo-list">
      <li v-for="(t, i) in todos" :key="i" class="todo-item" :class="'s-' + t.status">
        <span class="todo-mark" aria-hidden="true">
          <Icon v-if="t.status === 'completed'" name="check" :size="12" />
          <template v-else>{{ t.status === 'in_progress' ? '◐' : '○' }}</template>
        </span>
        <span class="todo-content">{{ t.content }}</span>
        <span class="todo-status">{{ statusLabel[t.status] || t.status }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
/* ---------- 收起态：竖排标签 ---------- */
.todo-tab {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 7px;
  width: 100%;
  padding: 12px 0;
  background: var(--bg-surface-2, rgba(255, 255, 255, 0.025));
  border: 1px solid var(--border);
  border-radius: 12px;
  color: var(--text-muted);
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;
}

.todo-tab:hover {
  border-color: var(--border-accent);
  color: var(--text);
}

.todo-tab.done {
  border-color: rgba(255, 154, 61, 0.25);
  color: var(--accent);
}

.tab-icon {
  color: var(--accent);
}

.tab-text {
  writing-mode: vertical-rl;
  font-size: 11.5px;
  letter-spacing: 0.18em;
}

.tab-count {
  font-size: 10.5px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.tab-chevron {
  color: var(--text-faint);
}

/* ---------- 展开态：完整卡片 ---------- */
.todo-card {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--bg-surface-2, rgba(255, 255, 255, 0.025));
  overflow: hidden;
  animation: todoIn 0.16s ease;
}

@keyframes todoIn {
  from {
    opacity: 0;
    transform: translateX(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.todo-head {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 9px 11px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  background: none;
  border: none;
  border-bottom: 1px solid var(--border);
  cursor: pointer;
  text-align: left;
  transition: color 0.15s ease;
}

.todo-head:hover {
  color: var(--text);
}

.todo-icon {
  width: 14px;
  height: 14px;
  flex: none;
}

.todo-count {
  margin-left: auto;
  color: var(--text-faint);
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}

.head-chevron {
  flex: none;
  color: var(--text-faint);
}

.todo-list {
  list-style: none;
  margin: 0;
  padding: 4px 0;
}

.todo-item {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  padding: 6px 11px;
  font-size: 12.5px;
  line-height: 1.45;
}

.todo-mark {
  flex: none;
  width: 14px;
  text-align: center;
  color: var(--accent);
  font-size: 11.5px;
  padding-top: 1px;
}

.todo-item.s-completed .todo-content {
  color: var(--text-faint);
  text-decoration: line-through;
}

.todo-item.s-in_progress .todo-content {
  color: var(--text);
  font-weight: 500;
}

.todo-content {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}

.todo-status {
  flex: none;
  font-size: 10.5px;
  color: var(--text-faint);
}

.todo-item.s-in_progress .todo-status {
  color: var(--accent);
}
</style>
