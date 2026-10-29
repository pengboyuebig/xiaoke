<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import type { Agent, Message, Project, ToolActivity } from '../types'
import MessageItem from './MessageItem.vue'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{
  messages: Message[]
  streaming: boolean
  streamingText: string
  liveTools: ToolActivity[]
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

// 依赖均为基本类型（长度/文本/布尔），避免返回新数组导致监听器永不触发
watch(
  () =>
    props.messages.length +
    ':' +
    props.streamingText.length +
    ':' +
    props.streaming +
    ':' +
    props.liveTools.length,
  stick,
  { flush: 'post' },
)
</script>

<template>
  <div ref="scroller" class="chat-view" @scroll="onScroll">
    <!-- 空状态欢迎页 -->
    <div v-if="!props.messages.length && !props.streaming && !props.liveTools.length" class="hero">
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

    <!-- 消息流 -->
    <div v-else class="thread">
      <template v-for="(m, i) in props.messages" :key="i">
        <MessageItem
          :role="m.role"
          :content="m.content"
          :tool-calls="m.toolCalls"
          :name="m.name"
          :avatar="m.role === 'assistant' ? agentAvatar() : undefined"
          :agent-name="m.role === 'assistant' ? agentName() : undefined"
        />
      </template>

      <!-- 流式进行中：已落定的工具结果 + 正在进行的工具 + 流式文本 -->
      <template v-if="props.streaming">
        <MessageItem
          v-for="(t, i) in props.liveTools"
          :key="'lt' + i"
          role="tool"
          :name="t.name"
          :content="t.done ? (t.preview || t.error || '(无输出)') : '正在调用…'"
        />
        <MessageItem
          role="assistant"
          :content="props.streamingText"
          :error="props.error"
          :avatar="agentAvatar()"
          :agent-name="agentName()"
          streaming
        />
      </template>
      <MessageItem v-else-if="props.error" role="assistant" content="" :error="props.error" :avatar="agentAvatar()" :agent-name="agentName()" />
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

.thread-end {
  height: 12px;
}
</style>
