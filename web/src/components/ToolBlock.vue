<script setup lang="ts">
import { computed, ref } from 'vue'
import { renderMarkdown } from '../lib/markdown'
import type { MsgGroup, ToolPair } from '../types'

const props = defineProps<{
  group: MsgGroup
  /** assistant 分组显示的名字（agent 名或「小柯」） */
  agentName?: string
  /** assistant 分组的头像（图片 URL 或 emoji） */
  avatar?: string
}>()

const expanded = ref(false)
const AVATAR_FALLBACK = '/assets/avatar.png'

const isImg = (v?: string) => !!v && (v.startsWith('/assets/') || v.startsWith('http'))
const agentAvatar = () => props.avatar || AVATAR_FALLBACK
const agentNameLabel = () => props.agentName || '小柯'

// 工具摘要行：⚙ list_files  src → ⚙app/  （第一行结果）
function argLabel(t: ToolPair): string {
  if (!t.arguments) return ''
  try {
    const o = JSON.parse(t.arguments)
    if (o.command && Array.isArray(o.args)) {
      return [o.command, ...o.args].join(' ')
    }
    const v = o.path ?? o.query ?? o.cwd ?? o.dir ?? ''
    return typeof v === 'string' ? v : ''
  } catch {
    const s = t.arguments.trim()
    return s.length > 40 ? s.slice(0, 40) + '…' : s
  }
}

// 结果摘要：第一行、去命令前缀、截断
function resultLabel(t: ToolPair): string {
  if (t.result === undefined) return '执行中…'
  const first = (t.result.split('\n').find((l) => l.trim()) || '').replace(/^\$\s*\S+\s*/, '').trim()
  const r = first || '(无输出)'
  return r.length > 60 ? r.slice(0, 60) + '…' : r
}

const sections = computed(() => props.group.sections || [])
// 正文若已以「**名字**」开头，渲染内已含名，避免与头部名字重复
const headerNameHidden = computed(() => /^\*\*[^*]+\*\*\s*/.test(sections.value[0] || ''))
const toolCount = computed(() => props.group.tools.length)

function render(md: string): string {
  return renderMarkdown(md)
}
</script>

<template>
  <!-- user：右侧气泡 -->
  <div v-if="props.group.kind === 'user'" class="msg user">
    <div class="body">
      <div class="meta"><span class="name">我</span></div>
      <div class="content user-bubble">{{ props.group.content }}</div>
    </div>
  </div>

  <!-- assistant / live：单头像 + 工具区块 + 正文段落 -->
  <div v-else class="msg assistant">
    <img
      v-if="isImg(agentAvatar())"
      class="avatar"
      :src="agentAvatar()"
      :alt="agentNameLabel()"
    />
    <span v-else class="avatar emoji-avatar">{{ agentAvatar() }}</span />

    <div class="body">
      <div v-if="!headerNameHidden" class="meta">
        <span class="name">{{ agentNameLabel() }}</span>
      </div>

      <!-- 工具调用：默认一行一个的紧凑摘要，点「N 次工具调用」展开/收起 -->
      <div v-if="toolCount" class="tool-block" :class="{ open: expanded }">
        <button class="tb-head" type="button" @click="expanded = !expanded">
          <span class="tb-count">⚙ {{ toolCount }} 次工具调用</span>
          <span class="tb-toggle">{{ expanded ? '收起 ▲' : '展开 ▼' }}</span>
        </button>

        <!-- 收起态：紧凑摘要行 -->
        <div v-if="!expanded" class="tb-compact">
          <div v-for="(t, i) in props.group.tools" :key="i" class="tc-row">
            <span class="tc-tool">{{ t.name }}</span>
            <span v-if="argLabel(t)" class="tc-arg">{{ argLabel(t) }}</span>
            <span v-if="t.result !== undefined" class="tc-arrow">→</span>
            <span v-if="t.result !== undefined" class="tc-res" :class="t.ok ? 'ok' : 'bad'">
              {{ resultLabel(t) }}
            </span>
          </div>
        </div>

        <!-- 展开态：完整参数与结果 -->
        <div v-else class="tb-detail">
          <div v-for="(t, i) in props.group.tools" :key="i" class="detail-item">
            <div class="di-head">
              <span class="di-tool">⚙ {{ t.name }}</span>
              <span class="di-status" :class="t.ok === false ? 'bad' : t.ok === true ? 'ok' : ''">
                {{ t.result === undefined ? '执行中…' : t.ok ? '✓ 成功' : '✗ 失败' }}
              </span>
            </div>
            <pre v-if="t.arguments" class="di-args">{{ t.arguments }}</pre>
            <pre v-if="t.result !== undefined" class="di-res" :class="t.ok ? 'ok' : 'bad'">{{ t.result }}</pre>
          </div>
        </div>
      </div>

      <!-- 正文段落（多段依次渲染） -->
      <div v-for="(s, i) in sections" :key="'s' + i" class="content md" v-html="render(s)"></div>

      <!-- live：思考中 / 光标 -->
      <div v-if="props.group.streaming && !sections.length && !toolCount" class="thinking">
        <span class="dot"></span><span class="dot"></span><span class="dot"></span>
      </div>
      <div v-else-if="props.group.streaming" class="cursor-row"><span class="cursor"></span></div>

      <div v-if="props.group.error" class="error-box">⚠ {{ props.group.error }}</div>
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

/* user 分组 */
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
  color: var(--accent);
}

/* ---------- 工具区块（默认收起） ---------- */
.tool-block {
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  margin-bottom: 8px;
  overflow: hidden;
}

.tb-head {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  background: none;
  border: none;
  color: var(--text-muted);
  font-size: 12px;
  padding: 7px 12px;
  cursor: pointer;
  transition: background 0.12s ease;
}

.tb-head:hover {
  background: var(--bg-surface);
}

.tb-count {
  color: var(--accent);
  font-weight: 600;
}

.tb-toggle {
  color: var(--text-faint);
  font-size: 11px;
}

/* 收起态摘要行 */
.tb-compact {
  padding: 2px 12px 9px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.tc-row {
  display: flex;
  align-items: baseline;
  gap: 7px;
  font-size: 12px;
  line-height: 1.7;
  min-width: 0;
}

.tc-tool {
  color: var(--accent);
  font-family: 'Cascadia Code', Consolas, monospace;
  flex-shrink: 0;
}

.tc-arg {
  color: var(--text-muted);
  font-family: 'Cascadia Code', Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.tc-arrow {
  color: var(--text-faint);
  flex-shrink: 0;
}

.tc-res {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.tc-res.ok {
  color: var(--ok);
}

.tc-res.bad {
  color: var(--danger);
}

/* 展开态详情 */
.tb-detail {
  border-top: 1px solid var(--border);
  padding: 8px 12px 10px;
  display: flex;
  flex-direction: column;
  gap: 9px;
  max-height: 400px;
  overflow-y: auto;
}

.detail-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.di-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.di-tool {
  font-size: 12px;
  color: var(--accent);
  font-weight: 600;
}

.di-status {
  font-size: 11px;
  color: var(--text-faint);
}

.di-status.ok {
  color: var(--ok);
}

.di-status.bad {
  color: var(--danger);
}

.di-args,
.di-res {
  margin: 0;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 7px 10px;
  font-size: 11.5px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 180px;
  overflow-y: auto;
  color: var(--text-muted);
}

.di-res.ok {
  border-left: 3px solid var(--ok);
}

.di-res.bad {
  border-left: 3px solid var(--danger);
  color: var(--danger);
}

/* 正文 */
.content {
  margin-top: 4px;
}

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
