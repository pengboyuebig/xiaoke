<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../lib/api'
import type { Project } from '../types'

const emit = defineEmits<{ close: []; created: [project: Project] }>()

const name = ref('')
const template = ref<'empty' | 'code' | 'article'>('empty')
const root = ref('')
const busy = ref(false)
const err = ref('')

async function create() {
  if (!name.value.trim() || busy.value) return
  busy.value = true
  err.value = ''
  try {
    const p = await api.createProject(name.value.trim(), template.value, root.value.trim() || undefined)
    emit('created', p)
    emit('close')
  } catch (e) {
    err.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!-- 遮罩不响应点击：只有右上角 ✕（或底部按钮）才能关闭，防止误触丢失输入 -->
  <div class="backdrop">
    <div class="modal">
      <div class="head">
        <span class="title">新建项目</span>
        <button class="x" @click="emit('close')">✕</button>
      </div>
      <div class="body">
        <div class="row">
          <label>项目名称（中文、字母、数字、-、_）</label>
          <input v-model="name" class="inp" placeholder="例如：我的博客 / demo-app" @keydown.enter="create" />
        </div>
        <div class="row">
          <label>模板</label>
          <div class="tpl-row">
            <button v-for="t in [
              { id: 'empty', label: '空项目' },
              { id: 'code', label: '代码（src/+docs/）' },
              { id: 'article', label: '文章（大纲+正文）' },
            ]" :key="t.id" class="chip" :class="{ active: template === t.id }" @click="template = t.id as 'empty' | 'code' | 'article'">
              {{ t.label }}
            </button>
          </div>
        </div>
        <div class="row">
          <label>创建位置（留空 = 默认项目根目录）</label>
          <input v-model="root" class="inp" placeholder="可选：自定义父目录的绝对路径" />
        </div>
        <div v-if="err" class="err">{{ err }}</div>
      </div>
      <div class="foot">
        <button class="btn ghost" @click="emit('close')">取消</button>
        <button class="btn primary" :disabled="busy || !name.trim()" @click="create">{{ busy ? '创建中…' : '创建并开始' }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}

.modal {
  width: min(460px, 92vw);
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.title {
  font-weight: 700;
  font-size: 15px;
}

.x {
  border: none;
  background: none;
  color: var(--text-muted);
  padding: 4px 8px;
  border-radius: var(--radius-sm);
}

.x:hover {
  background: var(--bg-surface-2);
  color: var(--text);
}

.body {
  padding: 16px 18px 6px;
}

.row {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin-bottom: 14px;
}

label {
  font-size: 12px;
  color: var(--text-muted);
}

.inp {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text);
  padding: 8px 10px;
  font-size: 13px;
}

.inp:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--focus-ring);
}

.tpl-row {
  display: flex;
  gap: 8px;
}

.chip {
  flex: 1;
  background: var(--bg);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  padding: 8px 6px;
  font-size: 12px;
}

.chip.active {
  border-color: var(--accent);
  color: var(--text);
  background: var(--accent-soft);
}

.err {
  color: var(--danger);
  font-size: 13px;
  margin-bottom: 10px;
}

.foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 18px;
  border-top: 1px solid var(--border);
}

.btn {
  border: none;
  border-radius: var(--radius-sm);
  padding: 8px 16px;
  font-size: 13px;
}

.btn.primary {
  background: var(--accent);
  color: var(--accent-text);
}

.btn.primary:hover:not(:disabled) {
  background: var(--accent-hover);
}

.btn.primary:disabled {
  opacity: 0.6;
}

.btn.ghost {
  background: var(--bg-surface-2);
  color: var(--text);
  border: 1px solid var(--border);
}
</style>
