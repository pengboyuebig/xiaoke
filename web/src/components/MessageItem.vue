<script setup lang="ts">
import { computed, onMounted, onUpdated, ref } from 'vue'
import hljs from 'highlight.js/lib/common'
import { renderMarkdown } from '../lib/markdown'
import type { ToolCallInfo } from '../types'

const props = defineProps<{
  role: 'user' | 'assistant' | 'tool'
  content: string
  streaming?: boolean
  error?: string
  stopped?: boolean
  toolCalls?: ToolCallInfo[]
  /** assistant 消息的头像（图片 URL 或 emoji），默认小柯 */
  avatar?: string
  /** assistant 消息的名字，默认「小柯」 */
  agentName?: string
}>()

const root = ref<HTMLElement | null>(null)
// 头像由 Go 后端的 /assets 提供动态绑定，避免 Vite 构建期解析
const AVATAR = '/assets/avatar.png'
const html = computed(() => (props.role === 'assistant' && props.content ? renderMarkdown(props.content) : ''))

// 流式增量渲染时，代码块每次更新都要重新高亮
function highlight() {
  if (props.role !== 'assistant') return
  root.value?.querySelectorAll('pre code:not([data-highlighted])').forEach((el) => {
    hljs.highlightElement(el as HTMLElement)
  })
}
onMounted(highlight)
onUpdated(highlight)

// write_file 之类调用的参数里提取目标路径做摘要
function argSummary(argsJson: string): string {
  try {
    const o = JSON.parse(argsJson)
    return o.path || o.command || o.query || o.dir || ''
  } catch {
    return ''
  }
}

function isImg(v?: string): boolean {
  return !!v && (v.startsWith('/assets/') || v.startsWith('http'))
}
</script>

<template>
  <!-- role=tool：工具结果行（折叠） -->
  <div v-if="props.role === 'tool'" class="tool-row">
    <details>
      <summary>
        <span class="t-name">⚙ {{ props.name }}</span>
        <span class="t-preview">{{ props.content.split('\n')[0].slice(0, 80) }}</span>
      </summary>
      <pre class="t-detail">{{ props.content }}</pre>
    </details>
  </div>

  <!-- user：靠右、橙色调气泡，一眼可辨是「我」 -->
  <div v-else-if="props.role === 'user'" class="msg user">
    <div class="body">
      <div class="meta"><span class="name">我</span></div>
      <div class="content user-bubble">{{ props.content }}</div>
    </div>
  </div>

  <!-- assistant：靠左、智能体头像 -->
  <div v-else ref="root" class="msg assistant">
    <img v-if="isImg(props.avatar)" class="avatar" :src="props.avatar" :alt="props.agentName || '小柯'" />
    <span v-else class="avatar emoji-avatar">{{ props.avatar || '🤖' }}</span>
    <div class="body">
      <div class="meta"><span class="name">{{ props.agentName || '小柯' }}</span></div>

      <!-- assistant 携带的工具调用 chips -->
      <div v-if="props.toolCalls?.length" class="calls">
        <div v-for="c in props.toolCalls" :key="c.id" class="call-chip">
          <span class="chip-name">⚙ {{ c.name }}</span>
          <span v-if="argSummary(c.arguments)" class="chip-arg">{{ argSummary(c.arguments) }}</span>
        </div>
      </div>

      <div v-if="props.content" class="content md" v-html="html"></div>

      <div v-if="props.streaming && !props.content && !props.toolCalls?.length" class="thinking">
        <span class="dot"></span><span class="dot"></span><span class="dot"></span>
      </div>
      <div v-else-if="props.streaming && props.content" class="cursor-row"><span class="cursor"></span></div>
      <div v-if="props.stopped" class="stopped-hint">已停止 · 这条回复未保存</div>
      <div v-if="props.error" class="error-box">⚠ {{ props.error }}</div>
    </div>
  </div>
</template>

<style scoped>
.msg {
  display: flex;
  gap: 12px;
  padding: 14px 0;
}

.avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  object-fit: cover;
  object-position: center 26%;
  flex-shrink: 0;
  border: 1px solid var(--border-strong);
}

.emoji-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  background: var(--bg-surface-2);
}

/* 用户消息：整体靠右，body 右对齐，气泡橙色调 */
.msg.user {
  justify-content: flex-end;
}

.msg.user .body {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.msg.user .meta {
  text-align: right;
}

.msg.user .name {
  color: var(--accent);
}

.body {
  min-width: 0;
  flex: 1;
}

.meta {
  margin-bottom: 2px;
}

.name {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}

.msg.assistant .name {
  color: var(--accent);
}

.user-bubble {
  display: inline-block;
  max-width: 72%;
  background: color-mix(in srgb, var(--accent) 16%, var(--bg-surface));
  border: 1px solid color-mix(in srgb, var(--accent) 38%, transparent);
  color: var(--text);
  border-radius: var(--radius-lg);
  border-top-right-radius: var(--radius-sm);
  padding: 8px 14px;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 工具调用 chips */
.calls {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 6px;
}

.call-chip {
  display: inline-flex;
  gap: 8px;
  align-items: baseline;
  font-size: 12px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 3px 9px;
  width: fit-content;
}

.chip-name {
  color: var(--accent);
}

.chip-arg {
  color: var(--text-faint);
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 工具结果行 */
.tool-row {
  padding: 2px 0 2px 46px;
}

.tool-row details summary {
  cursor: pointer;
  display: flex;
  gap: 10px;
  align-items: baseline;
  font-size: 12px;
  color: var(--text-faint);
  list-style: none;
}

.tool-row details summary::-webkit-details-marker {
  display: none;
}

.tool-row details summary::before {
  content: '▸';
  color: var(--text-faint);
}

.tool-row details[open] summary::before {
  content: '▾';
}

.t-name {
  color: var(--accent);
  flex-shrink: 0;
}

.t-preview {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.t-detail {
  margin: 6px 0 0;
  padding: 10px 12px;
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 12px;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 260px;
  overflow-y: auto;
}

/* Markdown 内容样式 */
.content :deep(p) {
  margin: 6px 0;
}

.content :deep(h1),
.content :deep(h2),
.content :deep(h3),
.content :deep(h4) {
  margin: 14px 0 6px;
  line-height: 1.4;
}

.content :deep(ul),
.content :deep(ol) {
  margin: 6px 0;
  padding-left: 22px;
}

.content :deep(li) {
  margin: 3px 0;
}

.content :deep(a) {
  color: var(--accent-hover);
  text-decoration: none;
}

.content :deep(a:hover) {
  text-decoration: underline;
}

.content :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 12px;
  border-left: 3px solid var(--accent);
  background: var(--accent-soft);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
  color: var(--text-muted);
}

.content :deep(code) {
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 1px 5px;
  font-size: 13px;
  font-family: 'Cascadia Code', Consolas, 'JetBrains Mono', monospace;
}

.content :deep(pre) {
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 14px;
  overflow-x: auto;
  margin: 8px 0;
}

.content :deep(pre code) {
  background: none;
  border: none;
  padding: 0;
  font-size: 13px;
}

.content :deep(table) {
  border-collapse: collapse;
  margin: 8px 0;
}

.content :deep(th),
.content :deep(td) {
  border: 1px solid var(--border);
  padding: 5px 10px;
}

.content :deep(th) {
  background: var(--bg-surface);
}

.content :deep(hr) {
  border: none;
  border-top: 1px solid var(--border);
  margin: 12px 0;
}

/* 思考中动画 */
.thinking {
  display: inline-flex;
  gap: 5px;
  padding: 10px 0;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--accent);
  animation: bounce 1.2s infinite ease-in-out;
}

.dot:nth-child(2) {
  animation-delay: 0.15s;
}

.dot:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes bounce {
  0%,
  60%,
  100% {
    transform: translateY(0);
    opacity: 0.4;
  }
  30% {
    transform: translateY(-5px);
    opacity: 1;
  }
}

/* 流式光标 */
.cursor-row {
  height: 18px;
  display: flex;
  align-items: center;
}

.cursor {
  display: inline-block;
  width: 8px;
  height: 16px;
  background: var(--accent);
  animation: blink 0.9s steps(1) infinite;
  border-radius: 1px;
}

@keyframes blink {
  50% {
    opacity: 0;
  }
}

.stopped-hint {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-faint);
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
