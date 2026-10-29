<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MsgGroup, ToolPair } from '../types'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{
  /** 回合内的分组（assistant 工具块 + 正文） */
  groups: MsgGroup[]
  /** 回合开始时间（首条消息） */
  startTime?: string
  /** 回合结束时间（末条消息）；流式中为空表示进行中 */
  endTime?: string
  streaming?: boolean
  /** 流式中的实时工具与正文（并入本回合展示） */
  liveGroups?: MsgGroup[]
  agentName?: string
  avatar?: string
}>()

const AVATAR_FALLBACK = '/assets/avatar.png'
const isImg = (v?: string) => !!v && (v.startsWith('/assets/') || v.startsWith('http'))
const agentAvatar = () => props.avatar || AVATAR_FALLBACK
const agentNameLabel = () => props.agentName || '小柯'

// 展开状态：流式中默认展开（看实时进度），完成后自动收起为一行时长
const expanded = ref(props.streaming === true)
// 用户手动点过之后不再自动收起（尊重手动展开）
const touched = ref(false)
watch(
  () => props.streaming,
  (s) => {
    if (s) {
      expanded.value = true
      touched.value = false
    } else if (!touched.value) {
      // 工作完成 → 自动收起（图2 效果）
      expanded.value = false
    }
  },
  { immediate: true },
)

function toggle() {
  touched.value = true
  expanded.value = !expanded.value
}

const allGroups = computed<MsgGroup[]>(() => [
  ...(props.groups || []),
  ...(props.streaming ? props.liveGroups || [] : []),
])

// 工具调用（跨分组合并）
const allTools = computed<ToolPair[]>(() => allGroups.value.flatMap((g) => g.tools || []))
const toolCount = computed(() => allTools.value.length)
const toolRounds = computed(() => allGroups.value.filter((g) => (g.tools || []).length > 0).length)

// 工作时长（首条→末条；流式中实时走秒）
const now = ref(Date.now())
let timer: number | undefined
watch(
  () => props.streaming,
  (s) => {
    if (s && !timer) {
      timer = window.setInterval(() => (now.value = Date.now()), 1000)
    } else if (!s && timer) {
      window.clearInterval(timer)
      timer = undefined
    }
  },
  { immediate: true },
)

function durMs(): number {
  const start = props.startTime ? Date.parse(props.startTime) : NaN
  if (Number.isNaN(start)) return 0
  const end = props.endTime ? Date.parse(props.endTime) : now.value
  return Math.max(0, end - start)
}

function fmtDur(ms: number): string {
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s} 秒`
  return `${Math.floor(s / 60)} 分 ${s % 60} 秒`
}

const durText = computed(() => fmtDur(durMs()))

// 收起态显示的摘要：工具数 + 正文首行（若有）
const hasSections = computed(() => allGroups.value.some((g) => (g.sections || []).length > 0))
const firstLine = computed(() => {
  const g = allGroups.value.find((x) => (x.sections || []).length)
  const s = g?.sections?.[0] || ''
  const plain = s.replace(/\*\*|__|`|#\s*/g, '').trim()
  const line = plain.split('\n').find((l) => l.trim()) || ''
  return line.length > 60 ? line.slice(0, 60) + '…' : line
})

// 工具摘要行：name arg → 结果
function argLabel(t: ToolPair): string {
  if (!t.arguments) return ''
  try {
    const o = JSON.parse(t.arguments)
    if (o.command && Array.isArray(o.args)) return [o.command, ...o.args].join(' ')
    const v = o.path ?? o.query ?? o.cwd ?? o.dir ?? ''
    return typeof v === 'string' ? v : ''
  } catch {
    const s = t.arguments.trim()
    return s.length > 40 ? s.slice(0, 40) + '…' : s
  }
}

function resultLabel(t: ToolPair): string {
  if (t.result === undefined) return ''
  const first = (t.result.split('\n').find((l) => l.trim()) || '').replace(/^\$\s*\S+\s*/, '').trim()
  const r = first || '(无输出)'
  return r.length > 70 ? r.slice(0, 70) + '…' : r
}

function toolIcon(name: string): string {
  if (name.startsWith('mcp_')) return '🔌'
  if (name === 'run_command') return '⌨'
  if (name === 'write_file') return '✏️'
  if (name === 'read_file' || name === 'list_files' || name === 'search_files') return '🔍'
  return '⚙'
}

function render(md: string): string {
  return renderMarkdown(md)
}
</script>

<template>
  <div class="turn">
    <!-- 顶栏：工作时长，点击展开/收起工作明细 -->
    <button class="turn-head" type="button" @click="toggle()">
      <span v-if="props.streaming" class="th-live">
        <span class="live-dot"></span>
        工作中 · {{ durText }}
      </span>
      <span v-else class="th-done">已工作 {{ durText }}</span>
      <span class="th-meta" v-if="toolCount">{{ toolRounds }} 轮 · {{ toolCount }} 次工具</span>
      <span class="th-chevron">{{ expanded ? '▲' : '▼' }}</span>
    </button>

    <!-- 明细：实时时间线（工作中）+ 完整参数与结果 -->
    <div v-if="expanded" class="turn-detail">
      <!-- 工具时间线 -->
      <div v-if="toolCount" class="tl">
        <div v-for="(t, i) in allTools" :key="'t' + i" class="tl-row">
          <span class="tl-icon">{{ toolIcon(t.name) }}</span>
          <span class="tl-tool">{{ t.name }}</span>
          <span v-if="argLabel(t)" class="tl-arg">{{ argLabel(t) }}</span>
          <span v-if="t.result !== undefined" class="tl-arrow">→</span>
          <span v-if="t.result !== undefined" class="tl-res" :class="t.ok ? 'ok' : 'bad'">
            {{ t.ok ? resultLabel(t) : '✗ 失败' }}
          </span>
          <span v-else class="tl-run">运行中…</span>
        </div>
      </div>

      <!-- 每轮的过渡说明与正文 -->
      <div v-for="(g, gi) in allGroups" :key="'g' + gi">
        <div v-for="(s, si) in g.sections || []" :key="'s' + si" class="turn-text md" v-html="render(s)"></div>
      </div>

      <!-- 展开态完整参数/结果（与收起时间线互补：这里是可复制的原文） -->
      <details v-if="toolCount" class="raw">
        <summary>查看完整参数与结果</summary>
        <div v-for="(t, i) in allTools" :key="'r' + i" class="raw-item">
          <div class="ri-head">
            <span class="ri-tool">⚙ {{ t.name }}</span>
            <span class="ri-status" :class="t.ok === false ? 'bad' : t.ok === true ? 'ok' : ''">
              {{ t.result === undefined ? '执行中…' : t.ok ? '✓ 成功' : '✗ 失败' }}
            </span>
          </div>
          <pre v-if="t.arguments" class="ri-args">{{ t.arguments }}</pre>
          <pre v-if="t.result !== undefined" class="ri-res" :class="t.ok ? 'ok' : 'bad'">{{ t.result }}</pre>
        </div>
      </details>

      <!-- 错误 -->
      <div v-for="(g, gi) in allGroups" :key="'e' + gi">
        <div v-if="g.error" class="error-box">⚠ {{ g.error }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.turn {
  margin: 6px 0 14px;
}

/* ---------- 顶栏 ---------- */
.turn-head {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 9px 14px;
  cursor: pointer;
  font-size: 13px;
  color: var(--text-muted);
  transition: border-color 0.15s ease, background 0.15s ease;
}

.turn-head:hover {
  border-color: var(--border-strong);
  background: var(--bg-surface-2);
}

.th-live {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--accent);
  font-weight: 600;
}

.live-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  animation: pulse 1.1s infinite ease-in-out;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.4;
    transform: scale(0.8);
  }
}

.th-done {
  color: var(--text-muted);
  font-weight: 500;
}

.th-meta {
  color: var(--text-faint);
  font-size: 12px;
}

.th-chevron {
  margin-left: auto;
  color: var(--text-faint);
  font-size: 11px;
}

/* ---------- 明细 ---------- */
.turn-detail {
  margin-top: 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-inset);
  padding: 10px 14px;
  max-height: 420px;
  overflow-y: auto;
}

/* 时间线 */
.tl {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.tl-row {
  display: flex;
  align-items: baseline;
  gap: 7px;
  font-size: 12.5px;
  line-height: 1.75;
  min-width: 0;
}

.tl-icon {
  flex-shrink: 0;
  width: 16px;
  text-align: center;
  font-size: 12px;
}

.tl-tool {
  color: var(--accent);
  font-family: 'Cascadia Code', Consolas, monospace;
  flex-shrink: 0;
}

.tl-arg {
  color: var(--text-muted);
  font-family: 'Cascadia Code', Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.tl-arrow {
  color: var(--text-faint);
  flex-shrink: 0;
}

.tl-res {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.tl-res.ok {
  color: var(--ok);
}

.tl-res.bad {
  color: var(--danger);
}

.tl-run {
  color: var(--text-faint);
  font-size: 11.5px;
}

/* 正文 */
.turn-text {
  margin-top: 8px;
  color: var(--text);
}

.turn-text :deep(p) {
  margin: 6px 0;
}

.turn-text :deep(ul),
.turn-text :deep(ol) {
  margin: 6px 0;
  padding-left: 22px;
}

.turn-text :deep(li) {
  margin: 3px 0;
}

.turn-text :deep(h1),
.turn-text :deep(h2),
.turn-text :deep(h3) {
  margin: 12px 0 6px;
  line-height: 1.4;
}

.turn-text :deep(a) {
  color: var(--accent-hover);
  text-decoration: none;
}

.turn-text :deep(code) {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 1px 5px;
  font-size: 12.5px;
  font-family: 'Cascadia Code', Consolas, monospace;
}

.turn-text :deep(pre) {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 10px 12px;
  overflow-x: auto;
  margin: 8px 0;
}

.turn-text :deep(pre code) {
  background: none;
  border: none;
  padding: 0;
}

.turn-text :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 12px;
  border-left: 3px solid var(--accent);
  background: var(--accent-soft);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  color: var(--text-muted);
}

.turn-text :deep(table) {
  border-collapse: collapse;
  margin: 8px 0;
}

.turn-text :deep(th),
.turn-text :deep(td) {
  border: 1px solid var(--border);
  padding: 5px 10px;
}

/* 完整原文折叠 */
.raw {
  margin-top: 10px;
  border-top: 1px solid var(--border);
  padding-top: 8px;
}

.raw summary {
  cursor: pointer;
  font-size: 12px;
  color: var(--text-faint);
  list-style: none;
}

.raw summary::-webkit-details-marker {
  display: none;
}

.raw summary::before {
  content: '▸ ';
}

.raw[open] summary::before {
  content: '▾ ';
}

.raw-item {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ri-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.ri-tool {
  font-size: 12px;
  color: var(--accent);
  font-weight: 600;
}

.ri-status {
  font-size: 11px;
  color: var(--text-faint);
}

.ri-status.ok {
  color: var(--ok);
}

.ri-status.bad {
  color: var(--danger);
}

.ri-args,
.ri-res {
  margin: 0;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 7px 10px;
  font-size: 11.5px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 160px;
  overflow-y: auto;
  color: var(--text-muted);
}

.ri-res.ok {
  border-left: 3px solid var(--ok);
}

.ri-res.bad {
  border-left: 3px solid var(--danger);
  color: var(--danger);
}

.error-box {
  margin-top: 8px;
  padding: 8px 12px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-sm);
  color: var(--danger);
  background: rgba(255, 107, 107, 0.08);
  font-size: 13px;
}
</style>
