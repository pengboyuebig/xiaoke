<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { api } from '../lib/api'
import type { FsDirsResp } from '../types'
import Icon from './Icon.vue'

const props = defineProps<{
  /** 初始进入的目录（空 = 默认项目根目录） */
  startPath?: string
  /** 拖拽进来的文件夹名：用于在当前目录列表里高亮提示 */
  highlight?: string
}>()

const emit = defineEmits<{
  /** 确认选择某个目录作为父目录 */
  pick: [path: string]
  close: []
}>()

const data = ref<FsDirsResp | null>(null)
const loading = ref(false)
const err = ref('')
/** 手动输入的路径（回车跳转） */
const manualPath = ref('')

async function load(path?: string) {
  if (loading.value) return
  loading.value = true
  err.value = ''
  try {
    data.value = await api.listDirs(path)
    manualPath.value = data.value.path
  } catch (e) {
    err.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

function goUp() {
  if (data.value?.parent) load(data.value.parent)
}

function enter(path: string) {
  load(path)
}

function pickCurrent() {
  if (data.value) emit('pick', data.value.path)
}

function submitManual() {
  const p = manualPath.value.trim()
  if (p) load(p)
}

onMounted(() => load(props.startPath))

// 外部改 startPath 时重新加载（例如从设置页带过来的路径）
watch(
  () => props.startPath,
  (v) => {
    if (v !== undefined && v !== data.value?.path) load(v)
  },
)
</script>

<template>
  <div class="backdrop">
    <div class="picker">
      <div class="head">
        <span class="title">
          <Icon name="folder" :size="15" :stroke="1.8" />
          选择文件夹
        </span>
        <button class="x" title="关闭" @click="emit('close')">
          <Icon name="x" :size="14" :stroke="2" />
        </button>
      </div>

      <!-- 路径栏：可手输绝对路径直接跳转 -->
      <div class="pathbar">
        <button class="nav" title="上一级" :disabled="!data?.parent || loading" @click="goUp">
          <Icon name="arrow-up" :size="14" :stroke="2" />
        </button>
        <input
          v-model="manualPath"
          class="inp path"
          placeholder="输入目录的绝对路径后回车"
          spellcheck="false"
          @keydown.enter="submitManual"
        />
        <button class="nav" title="刷新" :disabled="loading" @click="load(data?.path)">
          <Icon name="refresh" :size="14" :stroke="2" />
        </button>
      </div>

      <!-- 盘符快捷跳转（Windows） -->
      <div v-if="data?.roots?.length" class="roots">
        <button v-for="r in data.roots" :key="r" class="root-chip" :class="{ active: data.path === r }" @click="enter(r)">
          {{ r }}
        </button>
      </div>

      <div class="list">
        <div v-if="loading" class="hint">加载中…</div>
        <div v-else-if="err" class="err">{{ err }}</div>
        <div v-else-if="!data?.dirs.length" class="hint">这个目录下没有子文件夹。</div>
        <button
          v-for="d in data?.dirs ?? []"
          :key="d.path"
          class="dir-row"
          :class="{ match: highlight && d.name.toLowerCase() === highlight.toLowerCase() }"
          @click="enter(d.path)"
        >
          <Icon name="folder" :size="14" :stroke="1.8" />
          <span class="dir-name">{{ d.name }}</span>
          <span v-if="highlight && d.name.toLowerCase() === highlight.toLowerCase()" class="match-tag">拖入的</span>
          <Icon name="arrow-right" :size="13" :stroke="2" class="dir-go" />
        </button>
        <div v-if="data?.truncated" class="hint">子文件夹超过 500 个，仅显示前 500 个。</div>
      </div>

      <div class="foot">
        <span class="cur" :title="data?.path">{{ data?.path || '—' }}</span>
        <div class="btns">
          <button class="btn ghost" @click="emit('close')">取消</button>
          <button class="btn primary" :disabled="!data || loading" @click="pickCurrent">选择此文件夹</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  padding: 20px;
  background: rgba(5, 6, 9, 0.72);
  backdrop-filter: blur(10px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 60;
}

.picker {
  width: min(560px, 94vw);
  max-height: min(560px, 88vh);
  display: flex;
  flex-direction: column;
  background: linear-gradient(180deg, rgba(26, 30, 40, 0.97), rgba(16, 19, 26, 0.99));
  border: 1px solid var(--border-strong);
  border-radius: 18px;
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: pickerIn 0.2s var(--ease);
}

@keyframes pickerIn {
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
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.title {
  display: flex;
  align-items: center;
  gap: 7px;
  font-weight: 700;
  font-size: 14.5px;
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
}

.x:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: var(--border);
  color: var(--text);
}

.pathbar {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 12px 18px 8px;
}

.nav {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex: none;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid var(--border);
  border-radius: 9px;
  color: var(--text-muted);
}

.nav:hover:not(:disabled) {
  border-color: var(--border-strong);
  color: var(--text);
}

.nav:disabled {
  opacity: 0.35;
  cursor: default;
}

.inp {
  background: rgba(0, 0, 0, 0.28);
  border: 1px solid var(--border);
  border-radius: 9px;
  color: var(--text);
  padding: 7px 11px;
  font-size: 12px;
  font-family: var(--mono, ui-monospace, monospace);
}

.inp.path {
  flex: 1;
  min-width: 0;
}

.inp:focus {
  outline: none;
  border-color: var(--border-accent);
  box-shadow: var(--focus-ring);
}

.roots {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0 18px 8px;
}

.root-chip {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid var(--border);
  border-radius: 7px;
  color: var(--text-muted);
  padding: 4px 9px;
  font-size: 11.5px;
}

.root-chip:hover {
  border-color: var(--border-strong);
  color: var(--text);
}

.root-chip.active {
  border-color: rgba(255, 154, 61, 0.28);
  color: var(--accent);
  background: var(--accent-soft);
}

.list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 10px 10px;
  min-height: 120px;
}

.hint {
  padding: 14px 8px;
  color: var(--text-faint);
  font-size: 12px;
}

.err {
  padding: 14px 8px;
  color: var(--danger);
  font-size: 12px;
}

.dir-row {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  padding: 8px 10px;
  background: none;
  border: 1px solid transparent;
  border-radius: 9px;
  color: var(--text);
  font-size: 12.5px;
  text-align: left;
}

.dir-row:hover {
  background: rgba(255, 255, 255, 0.045);
  border-color: var(--border);
}

.dir-row.match {
  border-color: rgba(255, 154, 61, 0.4);
  background: var(--accent-soft);
}

.match-tag {
  flex: none;
  font-size: 10px;
  color: var(--accent);
  border: 1px solid rgba(255, 154, 61, 0.35);
  border-radius: 5px;
  padding: 1px 5px;
}

.dir-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dir-go {
  color: var(--text-faint);
  opacity: 0;
  transition: opacity 0.15s ease;
}

.dir-row:hover .dir-go {
  opacity: 1;
}

.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 18px;
  border-top: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.012);
}

.cur {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-faint);
  font-size: 11px;
  font-family: var(--mono, ui-monospace, monospace);
}

.btns {
  display: flex;
  gap: 8px;
  flex: none;
}
</style>
