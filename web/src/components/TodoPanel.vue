<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Todo } from '../types'
import TodoCard from './TodoCard.vue'

const props = defineProps<{
  todos: Todo[]
}>()

/** 面板是否展开；默认收起，用户点开才显示完整清单 */
const open = ref(false)
/** 用户手动操作过之后，就不再自动展开（尊重用户选择） */
const touched = ref(false)

// 首次出现待办时自动展开一次，让用户知道有这个清单；之后完全由用户控制
watch(
  () => props.todos.length,
  (n, prev) => {
    if (n > 0 && prev === 0 && !touched.value) open.value = true
  },
  { immediate: true },
)

function setOpen(v: boolean) {
  touched.value = true
  open.value = v
}

const hasTodos = computed(() => props.todos.length > 0)
</script>

<template>
  <aside v-if="hasTodos" class="todo-panel" :class="{ open }">
    <TodoCard :todos="todos" :open="open" @update:open="setOpen" />
  </aside>
</template>

<style scoped>
.todo-panel {
  flex: none;
  width: 52px;
  padding: 16px 10px 12px 0;
  transition: width 0.2s var(--ease, ease);
}

.todo-panel.open {
  width: 268px;
}

/* 窄屏：面板不占额外宽度，改为浮在聊天区右下角 */
@media (max-width: 1100px) {
  .todo-panel {
    width: 0;
    padding: 0;
  }

  .todo-panel.open {
    position: fixed;
    right: 16px;
    bottom: 92px;
    width: 262px;
    z-index: 30;
    padding: 0;
    filter: drop-shadow(0 12px 28px rgba(0, 0, 0, 0.45));
  }
}
</style>
