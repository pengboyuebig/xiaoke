<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Agent, Message, Project, Turn } from '../types'
import TurnView from './TurnView.vue'
import Icon from './Icon.vue'
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

const emit = defineEmits<{ approve: []; approveCustom: [text: string]; adjustPlan: []; quick: [text: string] }>()

// 空状态快捷提问：让欢迎页不只是一个大头像
const quickPrompts = ['帮我分析一下这个项目', '写一个网页小游戏', '帮我写一篇文章']

const scroller = ref<HTMLElement | null>(null)
const threadEl = ref<HTMLElement | null>(null)
const threadEnd = ref<HTMLElement | null>(null)
const pinned = ref(true)
// 头像由 Go 后端的 /assets 提供，动态绑定避免 Vite 构建期解析
const AVATAR = '/assets/avatar.png'

// ---- 回到底部 ----
function scrollToBottom() {
  // 直接写 scrollTop 在部分 WebView 会被拦截，用 scrollIntoView 更可靠
  const end = threadEnd.value
  if (end) end.scrollIntoView({ block: 'end', behavior: 'instant' })
  else if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
  pinned.value = true
}

// ---- 批准时自定义实现方式 ----
const customOpen = ref(false)
const customText = ref('')
function submitCustom() {
  const t = customText.value.trim()
  if (!t) return
  emit('approveCustom', t)
  customOpen.value = false
  customText.value = ''
}
function onCustomKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    submitCustom()
  }
}

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

// 用户手动上翻时停止自动跟随（同时控制「回到底部」按钮）
function onScroll() {
  const el = scroller.value
  if (!el) return
  pinned.value = el.scrollHeight - el.scrollTop - el.clientHeight < 90
}

async function stick() {
  if (!pinned.value) return
  await nextTick()
  // 用 threadEnd.scrollIntoView 而不是直接写 scrollTop：
  // 长会话首次渲染高度会分几帧增长，直接写 scrollHeight 容易停在半路
  const end = threadEnd.value
  if (end) end.scrollIntoView({ block: 'end', behavior: 'instant' })
  else if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
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
      <div class="hero-actions">
        <button v-for="q in quickPrompts" :key="q" class="hero-chip" @click="emit('quick', q)">
          <Icon name="sparkles" :size="13" />
          {{ q }}
        </button>
      </div>
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
      <!-- 计划已生成：批准后切开发模式并立即按计划开工；也可用自己的方式实现 -->
      <div v-if="props.awaitingApproval" class="approve-bar">
        <div class="ab-main">
          <span class="ab-icon"><Icon name="clipboard" :size="17" /></span>
          <span class="ab-text">计划已生成。批准后我会切到<b>开发模式</b>并立即按计划开工。</span>
        </div>
        <div class="ab-actions">
          <button class="ab-btn primary" @click="emit('approve')">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 6L9 17l-5-5" />
            </svg>
            批准并开始开发
          </button>
          <button class="ab-btn ghost" :class="{ active: customOpen }" @click="customOpen = !customOpen">
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
            </svg>
            我来说怎么做
          </button>
          <button class="ab-btn ghost" @click="emit('adjustPlan')">继续调整计划</button>
        </div>

        <!-- 自定义实现方式：写清楚你希望怎么改，批准后按你的要求开工 -->
        <div v-if="customOpen" class="ab-custom">
          <textarea
            v-model="customText"
            class="ab-input"
            rows="3"
            placeholder="例如：不要用 SVG，改用 Canvas 实现；动画只保留车轮转动；颜色用深蓝"
            @keydown="onCustomKeydown"
          ></textarea>
          <div class="ab-custom-foot">
            <span class="ab-hint">Ctrl+Enter 发送</span>
            <button class="ab-btn primary" :disabled="!customText.trim()" @click="submitCustom">
              按我的方式开工
            </button>
          </div>
        </div>
      </div>

      <div ref="threadEnd" class="thread-end"></div>
    </div>
    </div>

    <!-- 右下角浮动控件：回到底部 + 我的消息导航 -->
    <div v-if="showJump || (!pinned && !isEmpty)" ref="jumpWrap" class="jump-wrap">
      <!-- 回到底部：不在底部时出现 -->
      <button v-if="!pinned && !isEmpty" class="jump-bottom" title="回到底部" @click="scrollToBottom">
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 5v14M5 12l7 7 7-7" />
        </svg>
        回到底部
      </button>

      <template v-if="showJump">
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
      </template>
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
  scroll-padding-bottom: 32px;
}

/* ---------- 我的消息导航 ---------- */
.jump-wrap {
  position: absolute;
  right: 28px;
  bottom: 18px;
  z-index: 8;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}

.jump-list {
  width: 320px;
  max-height: 48vh;
  display: flex;
  flex-direction: column;
  background: linear-gradient(180deg, #1c202a 0%, #14171f 100%);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
  overflow: hidden;
}

.jl-head {
  padding: 11px 15px;
  font-size: 10.5px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-faint);
  border-bottom: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.025);
}

.jl-body {
  overflow-y: auto;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.jl-item {
  display: flex;
  align-items: baseline;
  gap: 9px;
  padding: 9px 10px;
  border-radius: 9px;
  border: none;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.jl-item:hover {
  background: rgba(255, 255, 255, 0.055);
}

.jl-idx {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.05);
  color: var(--text-faint);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
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
  font-size: 10px;
  color: var(--text-faint);
}

.jump-my,
.jump-bottom {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 9px 15px;
  border-radius: 999px;
  background: var(--bg-glass-strong);
  border: 1px solid var(--border-strong);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  box-shadow: var(--shadow-soft);
  backdrop-filter: blur(18px);
  transition:
    transform 0.15s var(--ease),
    border-color 0.15s ease,
    color 0.15s ease,
    background 0.15s ease;
}

.jump-my:hover,
.jump-my.open,
.jump-bottom:hover {
  border-color: var(--border-accent);
  color: var(--accent);
  background: var(--bg-surface-2);
  transform: translateY(-1px);
}

.jump-count {
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: var(--accent-gradient);
  color: var(--accent-text);
  font-size: 10px;
  font-weight: 700;
}

/* 跳转定位后的短暂高亮 */
.msg.user.jump-flash .user-bubble {
  animation: jumpGlow 1.4s ease;
}

@keyframes jumpGlow {
  0% {
    box-shadow: 0 0 0 4px rgba(255, 154, 61, 0.35), 0 10px 30px rgba(255, 154, 61, 0.16);
  }
  100% {
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
  }
}

/* ---------- 欢迎页 ---------- */
.hero {
  position: relative;
  max-width: var(--content-w);
  margin: 0 auto;
  padding: 12vh 24px 0;
  text-align: center;
}

.hero::before {
  content: '';
  position: absolute;
  left: 50%;
  top: 3vh;
  width: 460px;
  height: 460px;
  transform: translateX(-50%);
  background: radial-gradient(
    circle,
    rgba(255, 154, 61, 0.15),
    rgba(167, 139, 250, 0.05) 45%,
    transparent 68%
  );
  filter: blur(10px);
  pointer-events: none;
}

.hero > * {
  position: relative;
  z-index: 1;
}

.hero-avatar {
  width: 104px;
  height: 104px;
  border-radius: 50%;
  object-fit: cover;
  object-position: center 26%;
  border: 2px solid rgba(255, 154, 61, 0.6);
  box-shadow:
    0 0 0 8px rgba(255, 154, 61, 0.05),
    0 0 0 1px rgba(255, 255, 255, 0.08),
    0 24px 60px rgba(0, 0, 0, 0.5),
    0 0 70px rgba(255, 154, 61, 0.14);
}

.emoji-hero {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 52px;
  background: linear-gradient(150deg, var(--bg-surface-2), var(--bg-inset));
}

.hero-title {
  margin: 18px 0 4px;
  font-size: 32px;
  font-weight: 800;
  letter-spacing: 0.08em;
  background: linear-gradient(135deg, #fff 10%, #ffe0bd 55%, #ff9a3d 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.hero-sub {
  margin: 0 0 22px;
  color: var(--text-muted);
  font-size: 12.5px;
  letter-spacing: 0.02em;
}

.proj {
  color: var(--accent);
  font-weight: 600;
}

.hero-greeting {
  display: block;
  max-width: 560px;
  margin: 0 auto;
  text-align: left;
  background: linear-gradient(160deg, rgba(255, 255, 255, 0.055), rgba(255, 255, 255, 0.018));
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 15px 20px;
  color: var(--text-muted);
  box-shadow: var(--shadow-soft), var(--ring-inset);
  backdrop-filter: blur(16px);
  font-size: 13.5px;
  line-height: 1.7;
}

.hero-actions {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 18px;
}

.hero-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.03);
  color: var(--text-muted);
  font-size: 11.5px;
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background 0.15s ease,
    color 0.15s ease,
    transform 0.15s var(--ease);
}

.hero-chip:hover {
  border-color: var(--border-accent);
  background: rgba(255, 154, 61, 0.07);
  color: var(--text);
  transform: translateY(-1px);
}

.hero-chip :deep(.ic) {
  color: var(--accent);
}

.hero-hint {
  display: inline-block;
  margin: 14px 0 0;
  color: var(--text-faint);
  font-size: 11.5px;
  padding: 6px 14px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.02);
}

/* ---------- 消息流 ---------- */
.thread {
  max-width: var(--content-w);
  margin: 0 auto;
  padding: 26px 28px 10px;
}

.turn-item {
  margin-bottom: 10px;
}

/* 用户问题气泡（右对齐） */
.msg.user {
  display: flex;
  justify-content: flex-end;
  padding: 18px 0 6px;
}

.msg.user .body {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  max-width: 78%;
}

.msg.user .meta {
  text-align: right;
  margin-bottom: 4px;
}

.msg.user .name {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--accent);
  opacity: 0.85;
}

.user-bubble {
  display: inline-block;
  background: linear-gradient(135deg, rgba(255, 154, 61, 0.17), rgba(255, 154, 61, 0.065));
  border: 1px solid rgba(255, 154, 61, 0.23);
  color: var(--text);
  border-radius: 18px 18px 6px 18px;
  padding: 11px 16px;
  font-size: 13.5px;
  line-height: 1.68;
  white-space: pre-wrap;
  word-break: break-word;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18), inset 0 1px 0 rgba(255, 255, 255, 0.06);
}

.thread-end {
  height: 16px;
}

/* ---------- 计划批准条 ---------- */
.approve-bar {
  position: relative;
  overflow: hidden;
  margin: 8px 0 12px;
  padding: 16px 18px;
  border: 1px solid rgba(103, 232, 249, 0.22);
  border-radius: var(--radius-lg);
  background: linear-gradient(
    155deg,
    rgba(103, 232, 249, 0.095),
    rgba(167, 139, 250, 0.05) 55%,
    rgba(255, 255, 255, 0.02)
  );
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 13px;
  box-shadow: var(--shadow-soft);
  backdrop-filter: blur(16px);
}

.approve-bar::before {
  content: '';
  position: absolute;
  left: -40px;
  top: -60px;
  width: 180px;
  height: 180px;
  background: radial-gradient(circle, rgba(103, 232, 249, 0.18), transparent 70%);
  pointer-events: none;
}

.ab-main {
  position: relative;
  flex: 1;
  min-width: 220px;
  display: flex;
  align-items: center;
  gap: 11px;
  font-size: 13px;
  color: var(--text);
}

.ab-icon {
  width: 36px;
  height: 36px;
  border-radius: 11px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(103, 232, 249, 0.12);
  border: 1px solid rgba(103, 232, 249, 0.18);
  color: var(--cyan);
  flex-shrink: 0;
}

.ab-text b {
  color: var(--cyan);
}

.ab-actions {
  position: relative;
  display: flex;
  gap: 9px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.ab-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  border-radius: 10px;
  padding: 9px 16px;
  font-size: 12.5px;
  cursor: pointer;
  border: 1px solid transparent;
  transition:
    transform 0.15s var(--ease),
    border-color 0.15s ease,
    background 0.15s ease,
    color 0.15s ease;
  white-space: nowrap;
}

.ab-btn.primary {
  background: var(--accent-gradient);
  color: var(--accent-text);
  font-weight: 700;
  box-shadow: var(--shadow-accent);
}

.ab-btn.primary:hover {
  transform: translateY(-1px);
  filter: brightness(1.04);
}

.ab-btn.ghost {
  background: rgba(255, 255, 255, 0.04);
  border-color: var(--border);
  color: var(--text-muted);
}

.ab-btn.ghost:hover {
  border-color: var(--border-strong);
  color: var(--text);
  background: rgba(255, 255, 255, 0.06);
}

.ab-btn.ghost.active {
  border-color: rgba(103, 232, 249, 0.35);
  color: var(--cyan);
  background: rgba(103, 232, 249, 0.08);
}

/* 自定义实现方式输入区 */
.ab-custom {
  flex-basis: 100%;
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding-top: 12px;
  border-top: 1px dashed rgba(103, 232, 249, 0.22);
}

.ab-input {
  width: 100%;
  box-sizing: border-box;
  background: rgba(0, 0, 0, 0.28);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text);
  padding: 11px 13px;
  font-size: 13px;
  line-height: 1.65;
  resize: vertical;
  min-height: 74px;
  font-family: inherit;
}

.ab-input:focus {
  outline: none;
  border-color: rgba(103, 232, 249, 0.4);
  box-shadow: 0 0 0 3px rgba(103, 232, 249, 0.12);
}

.ab-custom-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.ab-hint {
  font-size: 10.5px;
  color: var(--text-faint);
}

.ab-btn.primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none;
  box-shadow: none;
}

@media (max-width: 760px) {
  .thread {
    padding: 18px 16px 8px;
  }

  .msg.user .body {
    max-width: 88%;
  }

  .jump-wrap {
    right: 14px;
    bottom: 12px;
  }

  .hero {
    padding-top: 8vh;
  }

  .hero-title {
    font-size: 27px;
  }
}
</style>
