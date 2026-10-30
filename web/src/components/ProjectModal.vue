<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../lib/api'
import type { Project } from '../types'
import Icon from './Icon.vue'

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
        <button class="x" @click="emit('close')" title="关闭">
          <Icon name="x" :size="14" :stroke="2" />
        </button>
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
  padding: 20px;
  background: rgba(5, 6, 9, 0.68);
  backdrop-filter: blur(10px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}

.modal {
  width: min(480px, 92vw);
  background: linear-gradient(180deg, rgba(26, 30, 40, 0.97), rgba(16, 19, 26, 0.99));
  border: 1px solid var(--border-strong);
  border-radius: 20px;
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: modalIn 0.22s var(--ease);
}

@keyframes modalIn {
  from {
    opacity: 0;
    transform: translateY(8px) scale(0.985);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}

.title {
  font-weight: 700;
  font-size: 15.5px;
  letter-spacing: -0.01em;
}

.x {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 1px solid transparent;
  background: none;
  color: var(--text-faint);
  border-radius: 8px;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.x:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: var(--border);
  color: var(--text);
}

.body {
  padding: 18px 20px 8px;
}

.row {
  display: flex;
  flex-direction: column;
  gap: 7px;
  margin-bottom: 15px;
}

label {
  font-size: 11.5px;
  color: var(--text-muted);
}

.inp {
  background: rgba(0, 0, 0, 0.28);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text);
  padding: 10px 12px;
  font-size: 12.5px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.inp:focus {
  outline: none;
  border-color: var(--border-accent);
  box-shadow: var(--focus-ring);
}

.tpl-row {
  display: flex;
  gap: 8px;
}

.chip {
  flex: 1;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: 10px;
  padding: 10px 8px;
  font-size: 11.5px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease;
}

.chip:hover {
  border-color: var(--border-strong);
  color: var(--text);
}

.chip.active {
  border-color: rgba(255, 154, 61, 0.28);
  color: var(--accent);
  background: var(--accent-soft);
}

.err {
  color: var(--danger);
  font-size: 12px;
  margin-bottom: 10px;
}

.foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.012);
}
</style>
