<script setup lang="ts">
import Icon from './Icon.vue'
import type { Todo } from '../types'

defineProps<{ todos: Todo[] }>()

const statusLabel: Record<string, string> = {
  pending: '待开始',
  in_progress: '进行中',
  completed: '已完成',
}
</script>

<template>
  <div v-if="todos.length" class="todo-card">
    <div class="todo-head">
      <Icon name="check" class="todo-icon" />
      <span>待办清单</span>
      <span class="todo-count">{{ todos.filter(t => t.status === 'completed').length }}/{{ todos.length }}</span>
    </div>
    <ul class="todo-list">
      <li v-for="(t, i) in todos" :key="i" class="todo-item" :class="'s-' + t.status">
        <span class="todo-mark" aria-hidden="true">
          <Icon v-if="t.status === 'completed'" name="check" />
          <template v-else>{{ t.status === 'in_progress' ? '◐' : '○' }}</template>
        </span>
        <span class="todo-content">{{ t.content }}</span>
        <span class="todo-status">{{ statusLabel[t.status] || t.status }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.todo-card {
  margin: 4px 0 10px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface-2, rgba(255, 255, 255, 0.02));
  overflow: hidden;
}
.todo-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border);
}
.todo-icon { width: 14px; height: 14px; }
.todo-count { margin-left: auto; color: var(--text-faint); font-weight: 400; }
.todo-list { list-style: none; margin: 0; padding: 4px 0; }
.todo-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  font-size: 13px;
  line-height: 1.45;
}
.todo-mark {
  flex: none;
  width: 16px;
  text-align: center;
  color: var(--accent);
  font-size: 12px;
}
.todo-item.s-completed .todo-content {
  color: var(--text-faint);
  text-decoration: line-through;
}
.todo-item.s-in_progress .todo-content { color: var(--text); font-weight: 500; }
.todo-content { flex: 1; min-width: 0; word-break: break-word; }
.todo-status {
  flex: none;
  font-size: 11px;
  color: var(--text-faint);
}
.todo-item.s-in_progress .todo-status { color: var(--accent); }
</style>
