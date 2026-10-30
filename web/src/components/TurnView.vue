<script setup lang="ts">
import { computed, onMounted, onUnmounted, onUpdated, ref, watch } from 'vue'
import hljs from 'highlight.js/lib/common'
import type { MsgGroup, ToolPair } from '../types'
import Icon from './Icon.vue'
import { renderMarkdownCollapsed } from '../lib/markdown'

const props = defineProps<{
  /** 回合内的分组（assistant 工具块 + 正文；流式实时组已由 App 并入） */
  groups: MsgGroup[]
  /** 回合开始时间（首条消息） */
  startTime?: string
  /** 回合结束时间（末条消息）；流式中为空表示进行中 */
  endTime?: string
  streaming?: boolean
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

const allGroups = computed<MsgGroup[]>(() => props.groups || [])

// 出错时顶栏常显错误（否则错误藏在收起的明细里，用户只看到「已工作 0 秒」）
const turnError = computed(() => allGroups.value.find((g) => g.error)?.error || '')

// 有可折叠内容（工具调用或错误）才显示时长行；纯问答回合直接展示回答
const hasWork = computed(() => toolCount.value > 0 || !!turnError.value)

// 工具调用（跨分组合并）
const allTools = computed<ToolPair[]>(() => allGroups.value.flatMap((g) => g.tools || []))
const toolCount = computed(() => allTools.value.length)
const toolRounds = computed(() => {
  const rounds = new Set<number>()
  for (const g of allGroups.value) {
    if ((g.tools || []).length > 0) rounds.add(g.round ?? 0)
  }
  return rounds.size
})

// 是否有可见正文（忽略只有空白字符的 delta）
const hasSections = computed(() =>
  allGroups.value.some((g) => (g.sections || []).some((s) => s.trim() !== '')),
)

// 流式中且没有工具/错误时显示状态胶囊：
// 首 token 前显示「正在思考」，文本开始输出后显示「正在输入」。
const showStatus = computed(() => props.streaming && !hasWork.value && !turnError.value)
const statusText = computed(() => (hasSections.value ? '正在输入' : '正在思考'))

// 流式结束但既没有正文也没有工具（模型空回复 / 首 token 前停止）
const isEmptyReply = computed(
  () =>
    !props.streaming &&
    !turnError.value &&
    toolCount.value === 0 &&
    !hasSections.value &&
    !!props.startTime,
)

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
// 组件销毁（删会话/切会话）时必须停表，否则定时器泄漏到已卸载组件
onUnmounted(() => {
  if (timer !== undefined) {
    window.clearInterval(timer)
    timer = undefined
  }
})

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

// 流式时组件会随每个 delta 重渲染；相同文本只解析一次，避免长回答越写越卡。
// 超大文本不缓存，防止一次长回答把缓存撑到几十 MB。
const MD_CACHE_MAX = 100
const MD_CACHE_MAX_CHARS = 64 * 1024
const mdCache = new Map<string, string>()

function render(md: string): string {
  if (md.length > MD_CACHE_MAX_CHARS) return renderMarkdownCollapsed(md)
  const hit = mdCache.get(md)
  if (hit !== undefined) return hit
  const out = renderMarkdownCollapsed(md)
  if (mdCache.size >= MD_CACHE_MAX) {
    const first = mdCache.keys().next().value
    if (first !== undefined) mdCache.delete(first)
  }
  mdCache.set(md, out)
  return out
}

// 工具参数/结果摘要的缓存：模板里同一行会多次调用，且每次渲染都会重算。
const ARG_CACHE_MAX = 500
const argCache = new Map<string, string>()
const RES_CACHE_MAX = 200
const resCache = new Map<string, string>()

function argLabel(t: ToolPair): string {
  if (!t.arguments) return ''
  const cacheable = t.arguments.length <= 4096
  if (cacheable) {
    const hit = argCache.get(t.arguments)
    if (hit !== undefined) return hit
  }
  let out = ''
  try {
    const o = JSON.parse(t.arguments)
    if (o.command && Array.isArray(o.args)) out = [o.command, ...o.args].join(' ')
    else {
      const v = o.path ?? o.query ?? o.cwd ?? o.dir ?? ''
      out = typeof v === 'string' ? v : ''
    }
  } catch {
    const s = t.arguments.trim()
    out = s.length > 40 ? s.slice(0, 40) + '…' : s
  }
  if (cacheable) {
    if (argCache.size >= ARG_CACHE_MAX) argCache.clear()
    argCache.set(t.arguments, out)
  }
  return out
}

function resultLabel(t: ToolPair): string {
  if (t.result === undefined) return ''
  const cacheable = t.result.length <= 16384
  if (cacheable) {
    const hit = resCache.get(t.result)
    if (hit !== undefined) return hit
  }
  const first = (t.result.split('\n').find((l) => l.trim()) || '').replace(/^\$\s*\S+\s*/, '').trim()
  const r = first || '(无输出)'
  const out = r.length > 70 ? r.slice(0, 70) + '…' : r
  if (cacheable) {
    if (resCache.size >= RES_CACHE_MAX) resCache.clear()
    resCache.set(t.result, out)
  }
  return out
}

function toolIcon(name: string): string {
  if (name.startsWith('mcp_')) return 'plug'
  if (name === 'run_command') return 'terminal'
  if (name === 'write_file') return 'edit'
  if (name === 'read_file') return 'file'
  if (name === 'list_files') return 'folder'
  if (name === 'search_files') return 'search'
  return 'settings'
}

// 代码高亮（内容为 v-html 注入，需要在挂载/更新后处理）
const rootEl = ref<HTMLElement | null>(null)
function highlight() {
  rootEl.value?.querySelectorAll('.turn-answer pre code:not([data-highlighted])').forEach((el) => {
    hljs.highlightElement(el as HTMLElement)
  })
}
onMounted(highlight)
onUpdated(highlight)
</script>

<template>
  <div ref="rootEl" class="turn">
    <!-- 顶栏：工作时长，点击展开/收起工作过程（纯问答回合无此项） -->
    <button v-if="hasWork" class="turn-head" type="button" @click="toggle()">
      <span v-if="props.streaming" class="th-live">
        <span class="live-dot"></span>
        工作中 · {{ durText }}
      </span>
      <span v-else-if="turnError" class="th-err"><Icon name="warning" :size="14" /> 出错了 · 点击查看详情</span>
      <span v-else class="th-done">已工作 {{ durText }}</span>
      <span class="th-meta" v-if="toolCount">{{ toolRounds }} 轮 · {{ toolCount }} 次工具</span>
      <span class="th-chevron" :class="{ up: expanded }">
        <Icon :name="expanded ? 'chevron-up' : 'chevron-down'" :size="14" />
      </span>
    </button>

    <!-- 错误全文常显（不随明细收起） -->
    <div v-if="turnError && !expanded" class="err-inline">
      <Icon name="warning" :size="14" />
      <span>{{ turnError }}</span>
    </div>

    <!-- 工作过程（可折叠）：工具时间线 + 完整参数结果 + 错误；没有内容时不渲染空框 -->
    <div v-if="expanded && (toolCount || turnError)" class="turn-detail">
      <!-- 工具时间线 -->
      <div v-if="toolCount" class="tl">
        <div v-for="(t, i) in allTools" :key="'t' + i" class="tl-row">
          <span class="tl-icon"><Icon :name="toolIcon(t.name)" :size="14" /></span>
          <span class="tl-tool">{{ t.name }}</span>
          <span v-if="argLabel(t)" class="tl-arg">{{ argLabel(t) }}</span>
          <span v-if="t.result !== undefined" class="tl-arrow"><Icon name="arrow-right" :size="12" /></span>
          <span v-if="t.result !== undefined" class="tl-res" :class="t.ok ? 'ok' : 'bad'">
            <Icon v-if="!t.ok" name="x" :size="12" />
            {{ t.ok ? resultLabel(t) : '失败' }}
          </span>
          <span v-else class="tl-run">运行中…</span>
        </div>
      </div>

      <!-- 完整参数/结果（可复制原文） -->
      <details v-if="toolCount" class="raw">
        <summary>查看完整参数与结果</summary>
        <div v-for="(t, i) in allTools" :key="'r' + i" class="raw-item">
          <div class="ri-head">
            <span class="ri-tool"><Icon name="settings" :size="12" /> {{ t.name }}</span>
            <span class="ri-status" :class="t.ok === false ? 'bad' : t.ok === true ? 'ok' : ''">
              <Icon :name="t.result === undefined ? 'clock' : t.ok ? 'check' : 'x'" :size="12" />
              {{ t.result === undefined ? '执行中…' : t.ok ? '成功' : '失败' }}
            </span>
          </div>
          <pre v-if="t.arguments" class="ri-args">{{ t.arguments }}</pre>
          <pre v-if="t.result !== undefined" class="ri-res" :class="t.ok ? 'ok' : 'bad'">{{ t.result }}</pre>
        </div>
      </details>

      <!-- 错误（展开态内） -->
      <div v-for="(g, gi) in allGroups" :key="'e' + gi">
        <div v-if="g.error" class="error-box">
          <Icon name="warning" :size="14" />
          <span>{{ g.error }}</span>
        </div>
      </div>
    </div>

    <!-- 最终回答正文：始终展示，不折叠 -->
    <div class="turn-answer">
      <div v-for="(g, gi) in allGroups" :key="'a' + gi">
        <div v-for="(s, si) in g.sections || []" :key="'s' + si" class="turn-text md" v-html="render(s)"></div>
      </div>
    </div>

    <!-- 流式状态：首 token 前「正在思考」，出字后「正在输入」 -->
    <div v-if="showStatus" class="turn-status" role="status" aria-live="polite">
      <span class="ts-agent">{{ agentNameLabel() }}</span>
      <span>{{ statusText }}</span>
      <span class="typing-dots" aria-hidden="true"><i></i><i></i><i></i></span>
    </div>

    <!-- 空回复兜底：模型没返回内容 / 首 token 前停止 -->
    <div v-if="isEmptyReply" class="turn-empty">没有收到内容，请重试</div>
  </div>
</template>

<style scoped>
.turn {
  margin: 4px 0 18px;
}

/* 回答正文区（始终可见） */
.turn-answer:empty {
  display: none;
}

.turn-answer:not(:first-child) {
  margin-top: 10px;
}

/* ---------- 流式状态 ---------- */
.turn-status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
  padding: 7px 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.4;
  box-shadow: var(--shadow-sm);
}

.ts-agent {
  color: var(--accent);
  font-weight: 600;
}

.typing-dots {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.typing-dots i {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--accent);
  opacity: 0.3;
  animation: typingBounce 1.2s infinite ease-in-out;
}

.typing-dots i:nth-child(2) {
  animation-delay: 0.15s;
}

.typing-dots i:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes typingBounce {
  0%,
  80%,
  100% {
    opacity: 0.25;
    transform: translateY(0);
  }
  40% {
    opacity: 1;
    transform: translateY(-2px);
  }
}

.turn-empty {
  margin-top: 8px;
  padding: 8px 12px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  color: var(--text-faint);
  font-size: 12px;
}

/* ---------- 顶栏 ---------- */
.turn-head {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 11px;
  padding: 7px 10px;
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text-muted);
  transition: background 0.15s ease, border-color 0.15s ease;
}

.turn-head:hover {
  background: rgba(255, 255, 255, 0.035);
  border-color: var(--border);
}

.th-live {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--accent);
  font-weight: 600;
}

.live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 10px rgba(255, 154, 61, 0.65);
  animation: pulse 1.4s infinite ease-in-out;
}

@keyframes pulse {
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

.th-done {
  color: var(--text-muted);
  font-weight: 500;
}

.th-err {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--danger);
  font-weight: 600;
}

.err-inline {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 6px 10px 0;
  padding: 9px 12px;
  border: 1px solid rgba(251, 113, 133, 0.24);
  border-radius: 10px;
  color: var(--danger);
  background: var(--danger-soft);
  font-size: 12.5px;
  line-height: 1.55;
}

.err-inline :deep(.ic) {
  margin-top: 2px;
}

.th-meta {
  color: var(--text-faint);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.th-chevron {
  margin-left: auto;
  display: flex;
  color: var(--text-faint);
}

/* ---------- 明细 ---------- */
.turn-detail {
  margin-top: 8px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.028), rgba(255, 255, 255, 0.01));
  padding: 13px 16px;
  max-height: 460px;
  overflow-y: auto;
  box-shadow: var(--shadow-sm), var(--ring-inset);
}

/* 时间线 */
.tl {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding-left: 18px;
}

.tl::before {
  content: '';
  position: absolute;
  left: 5px;
  top: 6px;
  bottom: 6px;
  width: 1px;
  background: linear-gradient(180deg, rgba(255, 154, 61, 0.32), rgba(255, 255, 255, 0.05));
}

.tl-row {
  position: relative;
  display: flex;
  align-items: baseline;
  gap: 7px;
  font-size: 12px;
  line-height: 1.75;
  min-width: 0;
}

.tl-row::before {
  content: '';
  position: absolute;
  left: -16px;
  top: 8px;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--bg-surface-3);
  border: 1px solid rgba(255, 255, 255, 0.18);
}

.tl-icon {
  flex-shrink: 0;
  width: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-faint);
  align-self: center;
}

.tl-tool {
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 11.5px;
  font-weight: 600;
  flex-shrink: 0;
}

.tl-arg {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.tl-arrow {
  display: flex;
  align-items: center;
  color: var(--text-faint);
  flex-shrink: 0;
  align-self: center;
}

.tl-res {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  font-size: 11.5px;
}

.tl-res.ok {
  color: var(--ok);
}

.tl-res.bad {
  color: var(--danger);
}

.tl-run {
  color: var(--text-faint);
  font-size: 11px;
}

/* 正文 */
.turn-text {
  margin-top: 9px;
  color: var(--text);
  font-size: 14.5px;
  line-height: 1.78;
}

.turn-text :deep(p) {
  margin: 8px 0;
}

.turn-text :deep(p:first-child) {
  margin-top: 0;
}

.turn-text :deep(strong) {
  color: #fff;
  font-weight: 600;
}

.turn-text :deep(ul),
.turn-text :deep(ol) {
  margin: 8px 0;
  padding-left: 22px;
}

.turn-text :deep(li) {
  margin: 4px 0;
}

.turn-text :deep(li::marker) {
  color: var(--accent);
}

.turn-text :deep(h1),
.turn-text :deep(h2),
.turn-text :deep(h3),
.turn-text :deep(h4) {
  margin: 16px 0 8px;
  line-height: 1.42;
  color: #fff;
  font-weight: 700;
}

.turn-text :deep(h1) {
  font-size: 20px;
}

.turn-text :deep(h2) {
  font-size: 17px;
}

.turn-text :deep(h3) {
  font-size: 15px;
}

.turn-text :deep(a) {
  color: var(--accent-hover);
  text-decoration: none;
  border-bottom: 1px solid rgba(255, 176, 102, 0.28);
  transition: border-color 0.15s ease;
}

.turn-text :deep(a:hover) {
  border-bottom-color: var(--accent-hover);
}

.turn-text :deep(code) {
  background: rgba(255, 255, 255, 0.055);
  border: 1px solid rgba(255, 255, 255, 0.07);
  border-radius: 6px;
  padding: 1px 6px;
  font-size: 12.5px;
  color: #ffd7b0;
}

.turn-text :deep(pre) {
  background: #0b0d12;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 12px;
  padding: 14px 16px;
  overflow-x: auto;
  margin: 10px 0;
  box-shadow: var(--shadow-sm);
}

.turn-text :deep(pre code) {
  background: none;
  border: none;
  padding: 0;
  color: inherit;
  font-size: 12.5px;
  line-height: 1.7;
}

.turn-text :deep(hr) {
  border: none;
  border-top: 1px solid var(--border);
  margin: 16px 0;
}

.turn-text :deep(img) {
  max-width: 100%;
  border-radius: 10px;
}

/* 长代码块折叠：默认只显示一行摘要 */
.turn-text :deep(.code-collapse) {
  margin: 10px 0;
  border: 1px solid var(--border);
  border-radius: 11px;
  background: rgba(255, 255, 255, 0.025);
  overflow: hidden;
}

.turn-text :deep(.code-collapse > summary) {
  cursor: pointer;
  padding: 9px 13px;
  font-size: 11.5px;
  color: var(--text-muted);
  list-style: none;
  user-select: none;
  transition: background 0.15s ease, color 0.15s ease;
}

.turn-text :deep(.code-collapse > summary::-webkit-details-marker) {
  display: none;
}

.turn-text :deep(.code-collapse > summary::before) {
  content: '›';
  display: inline-block;
  margin-right: 7px;
  color: var(--text-faint);
  transition: transform 0.15s ease;
}

.turn-text :deep(.code-collapse[open] > summary::before) {
  transform: rotate(90deg);
}

.turn-text :deep(.code-collapse > summary:hover) {
  background: rgba(255, 255, 255, 0.04);
  color: var(--text);
}

.turn-text :deep(.code-collapse pre) {
  margin: 0;
  border: none;
  border-top: 1px solid var(--border);
  border-radius: 0;
  max-height: 440px;
  overflow: auto;
  box-shadow: none;
  background: #090b10;
}

.turn-text :deep(blockquote) {
  margin: 10px 0;
  padding: 9px 14px;
  border-left: 3px solid var(--accent);
  background: var(--accent-soft);
  border-radius: 0 10px 10px 0;
  color: var(--text-muted);
}

.turn-text :deep(table) {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  margin: 10px 0;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  font-size: 13px;
}

.turn-text :deep(th),
.turn-text :deep(td) {
  border-bottom: 1px solid var(--border);
  border-right: 1px solid var(--border);
  padding: 8px 12px;
  text-align: left;
}

.turn-text :deep(th:last-child),
.turn-text :deep(td:last-child) {
  border-right: none;
}

.turn-text :deep(tr:last-child td) {
  border-bottom: none;
}

.turn-text :deep(th) {
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-muted);
  font-weight: 600;
}

/* 完整原文折叠 */
.raw {
  margin-top: 12px;
  border-top: 1px solid var(--border);
  padding-top: 10px;
}

.raw summary {
  cursor: pointer;
  font-size: 11.5px;
  color: var(--text-faint);
  list-style: none;
  user-select: none;
  transition: color 0.15s ease;
}

.raw summary:hover {
  color: var(--text-muted);
}

.raw summary::-webkit-details-marker {
  display: none;
}

.raw summary::before {
  content: '›';
  display: inline-block;
  margin-right: 6px;
  transition: transform 0.15s ease;
}

.raw[open] summary::before {
  transform: rotate(90deg);
}

.raw-item {
  margin-top: 9px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.ri-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.ri-tool {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11.5px;
  color: var(--accent);
  font-weight: 600;
}

.ri-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10.5px;
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
  background: rgba(255, 255, 255, 0.025);
  border: 1px solid var(--border);
  border-radius: 9px;
  padding: 8px 11px;
  font-family: var(--font-mono);
  font-size: 11px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 180px;
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
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 9px;
  padding: 9px 12px;
  border: 1px solid rgba(251, 113, 133, 0.24);
  border-radius: 9px;
  color: var(--danger);
  background: var(--danger-soft);
  font-size: 12.5px;
  line-height: 1.55;
}

.error-box :deep(.ic) {
  margin-top: 2px;
}

@media (max-width: 760px) {
  .turn-text {
    font-size: 14px;
  }

  .turn-detail {
    padding: 11px 12px;
  }
}
</style>
