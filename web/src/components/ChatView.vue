<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Agent, Message, Project, Turn } from '../types'
import TurnView from './TurnView.vue'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{
  turns: Turn[]
  messages: Message[]
  streaming: boolean
  streamingText: string
  liveTools: unknown[]
  error: string
  greeting: string
  emptyProjectHint?: string
  currentProject?: Project | null
  agent?: Agent | null
  /** 计划模式下计划已生成，等待用户批准 */
  awaitingApproval?: boolean
}>()

const emit = defineEmits<{ approve: []; adjustPlan: [] }>()

const scroller = ref<HTMLElement | null>(null)
const threadEl = ref<HTMLElement | null>(null)
const pinned = ref(true)
// 头像由 Go 后端的 /assets 提供，动态绑定避免 Vite 构建期解析
const AVATAR = '/assets/avatar.png'

// ---- 「我的消息」导航：列出我发过的所有提问，点哪条跳哪条 ----
const jumpOpen = ref(false)
const jumpWrap = ref<HTMLElement | null>(null)

function onDocClickJump(e: MouseEvent) {
  if (!jumpOpen.value) return
  if (jumpWrap.value && !jumpWrap.value.contains(e.target as Node)) jumpOpen.value = false
}
onMounted(() => document.addEventListener('mousedown', onDocClickJump))
onUnmounted(() => document.removeEventListener('mousedown', onDocClickJump))

// 我的提问列表（与 DOM 中 .msg.user 的渲染顺序一一对应）
const questionList = computed(() => {
  const list: { text: string; time: string }[] = []
  for (const t of props.turns) {
    const q = (t.question || '').trim()
    if (!q) continue
    list.push({ text: q, time: fmtClock(t.startTime) })
  }
  return list
})

// 至少 2 条提问才显示导航按钮（1 条时没有跳转必要）
const showJump = computed(() => questionList.value.length >= 2)

function fmtClock(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

// 直接查 DOM，避免用 ref 数组时切会话残留旧节点导致跳错位置
function questionEls(): HTMLElement[] {
  const root = threadEl.value
  if (!root) return []
  return Array.from(root.querySelectorAll<HTMLElement>('.msg.user'))
}

function jumpTo(index: number) {
  const el = questionEls()[index]
  if (!el) return
  // behavior:'instant' 显式瞬时——'auto' 会被 CSS scroll-behavior:smooth 覆盖成动画
  el.scrollIntoView({ block: 'center', behavior: 'instant' })
  el.classList.add('jump-flash')
  setTimeout(() => el.classList.remove('jump-flash'), 1400)
  jumpOpen.value = false
}

const agentName = () => props.agent?.name || '小柯'
const agentAvatar = () => props.agent?.avatar || AVATAR
const isImg = (v?: string) => !!v && (v.startsWith('/assets/') || v.startsWith('http'))
const greetingForAgent = () => {
  const g = props.greeting
  if (agentName() === '小柯') return g
  return g.replace(/\*\*小柯\*\*/, `**${agentName()}**`)
}

// 用户手动上翻时停止自动跟随
function onScroll() {
  const el = scroller.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 90
}

async function stick() {
  if (!pinned.value) return
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

// 依赖均为基本类型，避免返回新数组导致监听器永不触发
watch(
  () =>
    props.turns.length +
    ':' +
    props.streamingText.length +
    ':' +
    props.streaming +
    ':' +
    props.liveTools.length +
    ':' +
    props.turns.reduce((n, t) => n + (t.groups?.length || 0), 0),
  stick,
  { flush: 'post' },
)

// 消息变化后保持滚动跟随（不依赖 ref 数组）
const isEmpty = computed(() => !props.turns.length && !props.streaming)
</script>

<template>
  <div class="chat-shell">
    <div ref="scroller" class="chat-view" @scroll="onScroll">
    <!-- 空状态欢迎页 -->
    <div v-if="isEmpty" class="hero">
      <img v-if="isImg(agentAvatar())" class="hero-avatar" :src="agentAvatar()" :alt="agentName()" />
      <span v-else class="hero-avatar emoji-hero">{{ agentAvatar() }}</span>
      <h1 class="hero-title">{{ agentName() }}</h1>
      <p v-if="props.currentProject" class="hero-sub">
        正在项目 <b class="proj">{{ props.currentProject.name }}</b> 中工作
      </p>
      <p v-else class="hero-sub">你的专属智能体 · 有什么想问的尽管说</p>
      <div class="hero-greeting md" v-html="renderMarkdown(greetingForAgent())"></div>
      <p v-if="!props.currentProject && props.emptyProjectHint" class="hero-hint">{{ props.emptyProjectHint }}</p>
    </div>

    <!-- 回合流：每个回合 = 问题气泡 + 工作容器 -->
    <div v-else ref="threadEl" class="thread">
      <div v-for="(t, i) in props.turns" :key="i" class="turn-item">
        <!-- 我的问题（与 questionList 同条件：trim 后非空才渲染，保证索引一一对应） -->
        <div v-if="(t.question || '').trim()" class="msg user">
          <div class="body">
            <div class="meta"><span class="name">我</span></div>
            <div class="content user-bubble">{{ t.question }}</div>
          </div>
        </div>

        <!-- 小柯的工作回合 -->
        <TurnView
          :groups="t.groups"
          :start-time="t.startTime"
          :end-time="t.endTime"
          :streaming="t.streaming"
          :agent-name="agentName()"
          :avatar="agentAvatar()"
        />
      </div>
      <!-- 计划已生成：批准后切开发模式并立即按计划开工 -->
      <div v-if="props.awaitingApproval" class="approve-bar">
        <div class="ab-main">
          <span class="ab-icon">📋</span>
          <span class="ab-text">计划已生成。批准后我会切到<b>开发模式</b>并立即按计划开工。</span>
        </div>
        <div class="ab-actions">
          <button class="ab-btn primary" @click="emit('approve')">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6L9 17l-5-5" />
            </svg>
            批准并开始开发
          </button>
          <button class="ab-btn ghost" @click="emit('adjustPlan')">继续调整计划</button>
        </div>
      </div>

      <div class="thread-end"></div>
    </div>
    </div>

    <!-- 我的消息导航：列出我发过的全部提问，点哪条跳哪条 -->
    <div v-if="showJump" ref="jumpWrap" class="jump-wrap">
      <div v-if="jumpOpen" class="jump-list">
        <div class="jl-head">我的消息 · {{ questionList.length }} 条</div>
        <div class="jl-body">
          <button v-for="(q, i) in questionList" :key="i" class="jl-item" @click="jumpTo(i)">
            <span class="jl-idx">{{ i + 1 }}</span>
            <span class="jl-text" :title="q.text">{{ q.text }}</span>
            <span class="jl-time">{{ q.time }}</span>
          </button>
        </div>
      </div>
      <button class="jump-my" :class="{ open: jumpOpen }" title="查看我的全部消息" @click="jumpOpen = !jumpOpen">
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
        </svg>
        我的消息
        <span class="jump-count">{{ questionList.length }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.chat-shell {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.chat-view {
  flex: 1;
  overflow-y: auto;
  scroll-behavior: smooth;
}

/* ---------- 我的消息导航 ---------- */
.jump-wrap {
  position: absolute;
  right: 24px;
  bottom: 16px;
  z-index: 8;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}

.jump-list {
  width: 300px;
  max-height: 46vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
  overflow: hidden;
}

.jl-head {
  padding: 9px 14px;
  font-size: 12px;
  color: var(--text-faint);
  border-bottom: 1px solid var(--border);
  background: var(--bg-surface-2);
}

.jl-body {
  overflow-y: auto;
  padding: 5px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.jl-item {
  display: flex;
  align-items: baseline;
  gap: 9px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  border: none;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s ease;
}

.jl-item:hover {
  background: var(--accent-soft);
}

.jl-idx {
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--bg-surface-2);
  color: var(--text-faint);
  font-size: 10px;
}

.jl-text {
  flex: 1;
  min-width: 0;
  font-size: 12.5px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jl-time {
  flex-shrink: 0;
  font-size: 10.5px;
  color: var(--text-faint);
}

.jump-my {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 999px;
  background: var(--bg-surface-2);
  border: 1px solid var(--border-strong);
  color: var(--text-muted);
  font-size: 12.5px;
  cursor: pointer;
  box-shadow: var(--shadow);
  transition: all 0.15s ease;
}

.jump-my:hover,
.jump-my.open {
  border-color: var(--accent);
  color: var(--accent);
}

.jump-count {
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: var(--accent);
  color: var(--accent-text);
  font-size: 10.5px;
  font-weight: 700;
}

/* 跳转定位后的短暂高亮 */
.msg.user.jump-flash .user-bubble {
  animation: jumpGlow 1.4s ease;
}

@keyframes jumpGlow {
  0% {
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--accent) 55%, transparent);
  }
  100% {
    box-shadow: 0 0 0 0 transparent;
  }
}

.hero {
  max-width: var(--content-w);
  margin: 0 auto;
  padding: 12vh 24px 0;
  text-align: center;
}

.hero-avatar {
  width: 96px;
  height: 96px;
  border-radius: 50%;
  object-fit: cover;
  object-position: center 26%;
  border: 2px solid var(--accent);
  box-shadow: var(--shadow);
}

.emoji-hero {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 48px;
  background: var(--bg-surface-2);
}

.hero-title {
  margin: 16px 0 2px;
  font-size: 26px;
  letter-spacing: 2px;
}

.hero-sub {
  margin: 0 0 18px;
  color: var(--text-muted);
  font-size: 13px;
}

.proj {
  color: var(--accent);
}

.hero-greeting {
  display: inline-block;
  text-align: left;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 10px 16px;
  color: var(--text-muted);
}

.hero-hint {
  margin: 14px 0 0;
  color: var(--text-faint);
  font-size: 12px;
}

.thread {
  max-width: var(--content-w);
  margin: 0 auto;
  padding: 16px 24px 8px;
}

.turn-item {
  margin-bottom: 8px;
}

/* 用户问题气泡（右对齐） */
.msg.user {
  display: flex;
  justify-content: flex-end;
  padding: 10px 0 4px;
}

.msg.user .body {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  max-width: 72%;
}

.msg.user .meta {
  text-align: right;
  margin-bottom: 2px;
}

.msg.user .name {
  font-size: 12px;
  font-weight: 600;
  color: var(--accent);
}

.user-bubble {
  display: inline-block;
  background: color-mix(in srgb, var(--accent) 16%, var(--bg-surface));
  border: 1px solid color-mix(in srgb, var(--accent) 38%, transparent);
  color: var(--text);
  border-radius: var(--radius-lg);
  border-top-right-radius: var(--radius-sm);
  padding: 8px 14px;
  white-space: pre-wrap;
  word-break: break-word;
}

.thread-end {
  height: 12px;
}

/* ---------- 计划批准条 ---------- */
.approve-bar {
  margin: 6px 0 10px;
  padding: 13px 16px;
  border: 1px solid color-mix(in srgb, #56c2e6 45%, var(--border));
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, rgba(86, 194, 230, 0.12), rgba(86, 194, 230, 0.04));
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.ab-main {
  flex: 1;
  min-width: 220px;
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 13px;
  color: var(--text);
}

.ab-icon {
  font-size: 18px;
  flex-shrink: 0;
}

.ab-text b {
  color: #56c2e6;
}

.ab-actions {
  display: flex;
  gap: 9px;
  flex-shrink: 0;
}

.ab-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: var(--radius-sm);
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.15s ease;
  white-space: nowrap;
}

.ab-btn.primary {
  background: linear-gradient(135deg, var(--accent), var(--accent-hover));
  color: var(--accent-text);
  font-weight: 600;
  box-shadow: 0 2px 10px rgba(242, 106, 27, 0.3);
}

.ab-btn.primary:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 14px rgba(242, 106, 27, 0.42);
}

.ab-btn.ghost {
  background: var(--bg-surface-2);
  border-color: var(--border);
  color: var(--text-muted);
}

.ab-btn.ghost:hover {
  border-color: var(--border-strong);
  color: var(--text);
}
</style>
