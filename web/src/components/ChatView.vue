<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
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
}>()

const scroller = ref<HTMLElement | null>(null)
const pinned = ref(true)
// 头像由 Go 后端的 /assets 提供，动态绑定避免 Vite 构建期解析
const AVATAR = '/assets/avatar.png'

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

const isEmpty = computed(() => !props.turns.length && !props.streaming)
</script>

<template>
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
    <div v-else class="thread">
      <div v-for="(t, i) in props.turns" :key="i" class="turn-item">
        <!-- 我的问题 -->
        <div v-if="t.question" class="msg user">
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
      <div class="thread-end"></div>
    </div>
  </div>
</template>

<style scoped>
.chat-view {
  flex: 1;
  overflow-y: auto;
  scroll-behavior: smooth;
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
</style>
