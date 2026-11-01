<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../lib/api'
import type { FsDirEntry, Project } from '../types'
import Icon from './Icon.vue'
import DirPicker from './DirPicker.vue'

const emit = defineEmits<{ close: []; created: [project: Project] }>()

const name = ref('')
const template = ref<'empty' | 'code' | 'article'>('empty')
const root = ref('')
const busy = ref(false)
const err = ref('')
/** 文件夹选择器是否展开 */
const picking = ref(false)

// ---------- 拖拽文件夹 ----------
/** 正在拖拽悬停（高亮落区） */
const dragOver = ref(false)
/** 拖拽处理中（查找同名目录） */
const resolving = ref(false)
/** 拖入的文件夹名（多个候选时展示） */
const dragName = ref('')
/** 同名目录的候选列表（为空表示未在查找） */
const candidates = ref<FsDirEntry[]>([])
const candidatesTruncated = ref(false)
/** 拖入的文件夹没找到（显示「全盘搜索」按钮） */
const notFound = ref(false)

/** 从 DataTransfer 取出拖入项的名字（只认文件夹） */
function droppedFolderName(dt: DataTransfer | null): string {
  if (!dt) return ''
  // items 里能区分文件/文件夹（webkitGetAsEntry 是 WebKit 扩展，Chrome/Edge 支持）
  const items = Array.from(dt.items || [])
  for (const it of items) {
    if (it.kind !== 'file') continue
    const entry = (it as DataTransferItem & { webkitGetAsEntry?: () => FileSystemEntry | null }).webkitGetAsEntry?.()
    if (entry && entry.isDirectory) return entry.name
  }
  // 退化路径：某些浏览器/环境拿不到 entry，就用第一个文件名（后缀像文件则放弃）
  const f = dt.files?.[0]
  if (f && !/\.[a-z0-9]{1,8}$/i.test(f.name)) return f.name
  return ''
}

async function handleDrop(e: DragEvent) {
  dragOver.value = false
  if (resolving.value) return
  const folder = droppedFolderName(e.dataTransfer)
  if (!folder) {
    // 拖的是文件而非文件夹：不报错，静默忽略（用户可能手滑）
    return
  }
  dragName.value = folder
  await lookup(folder, false)
}

/** 按名字查找文件夹；deep=true 时扫全盘（较慢，需用户主动触发） */
async function lookup(folder: string, deep: boolean) {
  resolving.value = true
  err.value = ''
  candidates.value = []
  try {
    const res = await api.findDir(folder, deep)
    candidatesTruncated.value = res.truncated
    if (res.dirs.length === 1) {
      // 唯一匹配：直接回填，省一次点击
      root.value = res.dirs[0].path
      candidates.value = []
      notFound.value = false
    } else if (res.dirs.length > 1) {
      candidates.value = res.dirs
      notFound.value = false
    } else if (!deep) {
      // 常用位置没命中：直接打开目录选择器并带上名字，让用户自己定位
      // （全盘搜索太慢，不作为默认路径）
      notFound.value = true
      err.value = `常用位置没找到「${folder}」。已在下方打开目录选择器，可直接在里面找；也可以点「全盘搜索」自动找。`
      picking.value = true
    } else {
      notFound.value = true
      err.value = `全盘搜索也没找到名为「${folder}」的文件夹。请用「浏览」手动选择。`
    }
  } catch (e) {
    err.value = (e as Error).message
  } finally {
    resolving.value = false
  }
}

function deepSearch() {
  if (dragName.value) lookup(dragName.value, true)
}

function chooseCandidate(d: FsDirEntry) {
  root.value = d.path
  candidates.value = []
  notFound.value = false
}

function pickDir(path: string) {
  root.value = path
  picking.value = false
  notFound.value = false
}

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
          <div class="root-row">
            <input v-model="root" class="inp" placeholder="可选：自定义父目录的绝对路径" />
            <button class="btn ghost browse" title="浏览文件夹" @click="picking = true">
              <Icon name="folder" :size="14" :stroke="1.8" />
              浏览
            </button>
          </div>

          <!-- 拖拽落区：直接把文件夹拖进来即可回填路径 -->
          <div
            class="dropzone"
            :class="{ over: dragOver, busy: resolving, filled: !!root.trim() }"
            @dragenter.prevent="dragOver = true"
            @dragover.prevent="dragOver = true"
            @dragleave.prevent="dragOver = false"
            @drop.prevent="handleDrop"
          >
            <Icon :name="resolving ? 'refresh' : 'folder'" :size="15" :stroke="1.8" />
            <span v-if="resolving">正在查找「{{ dragName }}」…</span>
            <span v-else-if="dragOver">松手即可使用这个文件夹</span>
            <span v-else>把文件夹拖到这里，或点「浏览」选择</span>
          </div>

          <div v-if="root.trim()" class="root-hint">将创建在：<code>{{ root.trim() }}</code></div>

          <!-- 同名文件夹有多个候选时让用户挑 -->
          <div v-if="candidates.length" class="candidates">
            <div class="cand-title">找到 {{ candidates.length }} 个同名文件夹，选一个：</div>
            <button v-for="c in candidates" :key="c.path" class="cand-row" :title="c.path" @click="chooseCandidate(c)">
              <Icon name="folder" :size="13" :stroke="1.8" />
              <span class="cand-path">{{ c.path }}</span>
            </button>
            <div v-if="candidatesTruncated" class="cand-note">搜索范围有限，结果可能不全；也可用「浏览」手动定位。</div>
          </div>

          <!-- 常用位置没找到：提供全盘搜索（较慢，需主动触发） -->
          <div v-if="notFound && !resolving" class="notfound-row">
            <button class="btn ghost deep-btn" @click="deepSearch">
              <Icon name="search" :size="13" :stroke="2" />
              全盘搜索「{{ dragName }}」
            </button>
          </div>
        </div>
        <div v-if="err" class="err">{{ err }}</div>
      </div>
      <div class="foot">
        <button class="btn ghost" @click="emit('close')">取消</button>
        <button class="btn primary" :disabled="busy || !name.trim()" @click="create">{{ busy ? '创建中…' : '创建并开始' }}</button>
      </div>
    </div>
  </div>

  <!-- 文件夹选择器：选中后回填到「创建位置」 -->
  <DirPicker
    v-if="picking"
    :start-path="root.trim() || undefined"
    :highlight="dragName || undefined"
    @pick="pickDir"
    @close="picking = false"
  />
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

.root-row {
  display: flex;
  gap: 8px;
}

.root-row .inp {
  flex: 1;
  min-width: 0;
}

.browse {
  display: flex;
  align-items: center;
  gap: 5px;
  flex: none;
  white-space: nowrap;
}

.root-hint {
  font-size: 11px;
  color: var(--text-faint);
  overflow-wrap: anywhere;
}

.root-hint code {
  color: var(--accent);
  font-family: var(--mono, ui-monospace, monospace);
}

/* 拖拽落区 */
.dropzone {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 11px 12px;
  margin-top: 2px;
  border: 1px dashed var(--border-strong);
  border-radius: 10px;
  color: var(--text-faint);
  font-size: 11.5px;
  transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease;
}

.dropzone.over {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.dropzone.busy {
  border-style: solid;
  color: var(--text-muted);
}

.dropzone.filled {
  border-color: rgba(255, 154, 61, 0.28);
  color: var(--text-muted);
}

/* 同名候选列表 */
.candidates {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 9px 10px;
  background: rgba(0, 0, 0, 0.22);
  border: 1px solid var(--border);
  border-radius: 10px;
}

.cand-title {
  font-size: 11px;
  color: var(--text-muted);
  margin-bottom: 2px;
}

.cand-row {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  padding: 6px 8px;
  background: none;
  border: 1px solid transparent;
  border-radius: 7px;
  color: var(--text);
  font-size: 11.5px;
  text-align: left;
}

.cand-row:hover {
  background: rgba(255, 255, 255, 0.045);
  border-color: var(--border);
}

.cand-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--mono, ui-monospace, monospace);
}

.cand-note {
  font-size: 10.5px;
  color: var(--text-faint);
}

.notfound-row {
  display: flex;
}

.deep-btn {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11.5px;
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
