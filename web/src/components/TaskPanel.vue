<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../lib/api'
import type { TaskInfo } from '../types'
import { askConfirm } from '../lib/confirm'
import Icon from './Icon.vue'

// 后台任务面板：run_command is_background 起的真实进程（npm run dev 之类）。
// 页面上有个常驻标记，点开能看日志，也能直接关闭——关闭会按进程树终止，
// 端口随之释放（Windows 上 npm.cmd 拉起的 node 子进程会被一起杀掉）。

const tasks = ref<TaskInfo[]>([])
const open = ref(false)
const detailId = ref('')
const detailOutput = ref('')
const detailLoading = ref(false)
const killing = ref('')
const err = ref('')
let timer: number | undefined

const running = computed(() => tasks.value.filter((t) => t.state === 'running'))
const sorted = computed(() =>
  [...tasks.value].sort((a, b) => {
    const ar = a.state === 'running'
    const br = b.state === 'running'
    if (ar !== br) return ar ? -1 : 1
    return (b.startedAt || '').localeCompare(a.startedAt || '')
  }),
)

function fmtDur(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  if (s < 60) return `${s} 秒`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分 ${s % 60} 秒`
  return `${Math.floor(m / 60)} 时 ${m % 60} 分`
}

const stateLabel: Record<string, string> = {
  running: '运行中',
  completed: '已结束',
  failed: '失败',
}

async function refresh() {
  // 页面不可见时不轮询：切到后台标签就不用每 3 秒打一次接口
  if (document.hidden) return
  try {
    const r = await api.listTasks()
    tasks.value = r.tasks || []
    if (open.value && detailId.value) await loadDetail(detailId.value, true)
  } catch {
    /* 轮询失败不打扰用户 */
  }
}

async function loadDetail(id: string, quiet = false) {
  detailId.value = id
  if (!quiet) detailLoading.value = true
  try {
    const r = await api.taskDetail(id)
    detailOutput.value = r.output || '（暂无输出）'
  } catch (e) {
    detailOutput.value = (e as Error).message
  } finally {
    detailLoading.value = false
  }
}

async function openPanel() {
  open.value = true
  err.value = ''
  await refresh()
  const first = sorted.value[0]
  if (first) await loadDetail(first.id)
}

function closePanel() {
  open.value = false
  detailId.value = ''
  detailOutput.value = ''
}

async function killTask(t: TaskInfo) {
  const ok = await askConfirm({
    title: '关闭后台任务',
    message: `将终止「${t.cmdline}」及其子进程，占用的端口会随之释放。确定关闭？`,
    confirmText: '关闭任务',
  })
  if (!ok) return
  killing.value = t.id
  err.value = ''
  try {
    await api.killTask(t.id)
    await refresh()
    if (detailId.value === t.id) await loadDetail(t.id)
  } catch (e) {
    err.value = (e as Error).message
  } finally {
    killing.value = ''
  }
}

onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 3000)
})
onUnmounted(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
</script>

<template>
  <!-- 运行中标记：只有存在运行中的任务才出现（面板打开时也保持可见） -->
  <button
    v-if="running.length || open"
    class="task-chip"
    :class="{ live: running.length > 0 }"
    :title="running.length ? `${running.length} 个后台任务运行中，点击查看日志/关闭` : '后台任务'"
    @click="openPanel"
  >
    <span v-if="running.length" class="chip-dot" aria-hidden="true"></span>
    <Icon v-else name="terminal" :size="13" />
    <span class="chip-text">
      {{ running.length ? `${running.length} 个任务运行中` : '后台任务' }}
    </span>
  </button>

  <!-- 面板 -->
  <div v-if="open" class="backdrop" @click.self="closePanel">
    <div class="panel">
      <div class="head">
        <span class="title">
          <Icon name="terminal" :size="15" />
          后台任务
        </span>
        <button class="x" title="关闭面板" @click="closePanel">
          <Icon name="x" :size="14" :stroke="2" />
        </button>
      </div>

      <div class="body">
        <div class="list">
          <button
            v-for="t in sorted"
            :key="t.id"
            class="task-row"
            :class="{ active: t.id === detailId }"
            @click="loadDetail(t.id)"
          >
            <span class="tr-state" :class="t.state">{{ stateLabel[t.state] }}</span>
            <span class="tr-main">
              <span class="tr-cmd" :title="t.cmdline">{{ t.cmdline }}</span>
              <span class="tr-meta">{{ fmtDur(t.durationMs) }} · {{ t.id }}</span>
            </span>
          </button>
          <div v-if="!sorted.length" class="empty">当前没有后台任务。</div>
        </div>

        <div class="detail">
          <div class="detail-head">
            <span class="detail-title">
              {{ sorted.find((t) => t.id === detailId)?.cmdline || '日志' }}
            </span>
            <button
              v-if="sorted.find((t) => t.id === detailId)?.state === 'running'"
              class="btn danger tiny"
              :disabled="!!killing"
              title="终止该进程树，端口随之释放"
              @click="killTask(sorted.find((t) => t.id === detailId)!)"
            >
              <Icon name="trash" :size="12" />
              {{ killing === detailId ? '关闭中…' : '关闭任务' }}
            </button>
          </div>
          <div v-if="err" class="err">{{ err }}</div>
          <pre v-if="detailLoading" class="log">加载中…</pre>
          <pre v-else class="log">{{ detailOutput || '（暂无输出）' }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ---------- 运行中标记 ---------- */
.task-chip {
  position: fixed;
  left: calc(var(--sidebar-w) + 20px);
  bottom: 128px;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 13px;
  border-radius: 999px;
  border: 1px solid var(--border-strong);
  background: var(--bg-glass-strong, rgba(25, 29, 39, 0.9));
  color: var(--text-muted);
  font-size: 12px;
  box-shadow: var(--shadow-soft);
  backdrop-filter: blur(16px);
}

.task-chip.live {
  border-color: rgba(255, 154, 61, 0.4);
  color: var(--accent);
}

.task-chip:hover {
  border-color: var(--border-accent);
  color: var(--accent);
}

.chip-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 8px rgba(255, 154, 61, 0.6);
  animation: taskPulse 1.4s infinite ease-in-out;
}

@keyframes taskPulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.35;
    transform: scale(0.72);
  }
}

/* ---------- 面板 ---------- */
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 45;
  background: rgba(5, 6, 9, 0.6);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.panel {
  width: min(860px, 94vw);
  height: min(560px, 86vh);
  display: flex;
  flex-direction: column;
  background: linear-gradient(180deg, rgba(26, 30, 40, 0.98), rgba(16, 19, 26, 0.99));
  border: 1px solid var(--border-strong);
  border-radius: 18px;
  box-shadow: var(--shadow);
  overflow: hidden;
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
  gap: 8px;
  font-weight: 700;
  font-size: 14px;
}

.x {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
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

.body {
  flex: 1;
  min-height: 0;
  display: flex;
}

.list {
  width: 300px;
  flex: none;
  overflow-y: auto;
  border-right: 1px solid var(--border);
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.task-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 9px 10px;
  border: 1px solid transparent;
  border-radius: 10px;
  background: none;
  text-align: left;
  color: var(--text-muted);
}

.task-row:hover {
  background: rgba(255, 255, 255, 0.045);
}

.task-row.active {
  background: var(--accent-soft);
  border-color: rgba(255, 154, 61, 0.3);
  color: var(--text);
}

.tr-state {
  flex: none;
  font-size: 9.5px;
  padding: 2px 5px;
  border-radius: 5px;
  border: 1px solid var(--border);
  color: var(--text-faint);
  margin-top: 1px;
}

.tr-state.running {
  color: var(--accent);
  border-color: rgba(255, 154, 61, 0.35);
}

.tr-state.failed {
  color: var(--danger);
  border-color: var(--danger-soft);
}

.tr-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.tr-cmd {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tr-meta {
  font-size: 10px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.detail {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 10px 12px 12px;
  gap: 8px;
}

.detail-head {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
}

.detail-title {
  flex: 1;
  min-width: 0;
  font-size: 11.5px;
  color: var(--text-muted);
  font-family: var(--mono, ui-monospace, monospace);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log {
  flex: 1;
  min-height: 0;
  margin: 0;
  overflow: auto;
  padding: 10px 12px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid var(--border);
  color: var(--text-muted);
  font-size: 11.5px;
  line-height: 1.55;
  font-family: var(--mono, ui-monospace, monospace);
  white-space: pre-wrap;
  word-break: break-word;
}

.empty {
  padding: 12px 10px;
  font-size: 11.5px;
  color: var(--text-faint);
}

.err {
  font-size: 11.5px;
  color: var(--danger);
}
</style>
