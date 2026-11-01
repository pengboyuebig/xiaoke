<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from './Icon.vue'

const props = defineProps<{
  /** 当前等待回答的请求；null = 关闭 */
  request: {
    id: string
    kind: 'question' | 'permission'
    question?: string
    options?: string[]
    multiSelect?: boolean
    tool?: string
    summary?: string
  } | null
}>()

const emit = defineEmits<{ answer: [answer: string] }>()

const selected = ref<Set<string>>(new Set())
const freeText = ref('')
const useFree = ref(false)

watch(
  () => props.request?.id,
  () => {
    selected.value = new Set()
    freeText.value = ''
    useFree.value = !props.request?.options?.length
  },
)

const isPermission = computed(() => props.request?.kind === 'permission')
const canSubmit = computed(() => {
  if (useFree.value) return freeText.value.trim().length > 0
  if (isPermission.value) return true // 权限三按钮直接提交
  return selected.value.size > 0
})

function toggle(option: string) {
  if (props.request?.multiSelect) {
    const next = new Set(selected.value)
    if (next.has(option)) next.delete(option)
    else next.add(option)
    selected.value = next
  } else {
    selected.value = new Set([option])
  }
}

function submit() {
  if (!canSubmit.value) return
  const answer = useFree.value
    ? freeText.value.trim()
    : [...selected.value].join('、')
  emit('answer', answer)
}

function answerPermission(a: string) {
  emit('answer', a)
}
</script>

<template>
  <div v-if="request" class="ask-overlay">
    <div class="ask-card" role="dialog" aria-modal="true">
      <!-- 权限审批 -->
      <template v-if="isPermission">
        <div class="ask-head perm">
          <span class="ask-icon"><Icon name="lock" :size="17" /></span>
          <span>小柯请求执行 <b>{{ request.tool }}</b></span>
        </div>
        <pre class="ask-summary">{{ request.summary }}</pre>
        <div class="ask-actions">
          <button class="ab-btn primary" @click="answerPermission('once')">仅此一次</button>
          <button class="ab-btn ghost" @click="answerPermission('session')">本会话允许</button>
          <button class="ab-btn danger" @click="answerPermission('deny')">拒绝</button>
        </div>
      </template>

      <!-- 提问 -->
      <template v-else>
        <div class="ask-head">
          <span class="ask-icon"><Icon name="message" :size="17" /></span>
          <span>小柯有一个问题</span>
        </div>
        <p class="ask-question">{{ request.question }}</p>
        <div v-if="request.options?.length" class="ask-options">
          <button
            v-for="opt in request.options"
            :key="opt"
            class="ask-opt"
            :class="{ active: selected.has(opt) }"
            @click="toggle(opt)"
          >
            <span class="ao-mark">{{ request.multiSelect ? (selected.has(opt) ? '☑' : '☐') : selected.has(opt) ? '◉' : '○' }}</span>
            {{ opt }}
          </button>
        </div>
        <div class="ask-free">
          <label class="af-toggle">
            <input v-model="useFree" type="checkbox" />
            自由输入
          </label>
          <textarea
            v-if="useFree || !request.options?.length"
            v-model="freeText"
            class="af-text"
            rows="2"
            placeholder="用自己的话回答…"
            @keydown.enter.exact.prevent="submit"
          ></textarea>
        </div>
        <div class="ask-actions">
          <button class="ab-btn primary" :disabled="!canSubmit" @click="submit">回答</button>
          <button class="ab-btn ghost" @click="emit('answer', '')">跳过</button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ask-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 90;
}
.ask-card {
  width: min(520px, calc(100vw - 40px));
  background: var(--surface, #15161a);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 18px 20px;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.45);
}
.ask-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 12px;
}
.ask-head.perm { color: #fbbf24; }
.ask-icon { display: inline-flex; color: var(--accent); }
.ask-question {
  margin: 0 0 12px;
  font-size: 14px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.ask-summary {
  margin: 0 0 14px;
  padding: 10px 12px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 12px;
  color: var(--text-muted);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 180px;
  overflow: auto;
}
.ask-options {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;
}
.ask-opt {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: transparent;
  color: var(--text);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}
.ask-opt:hover { border-color: var(--accent); }
.ask-opt.active {
  border-color: var(--accent);
  background: rgba(103, 232, 249, 0.08);
}
.ao-mark { color: var(--accent); }
.ask-free { margin-bottom: 12px; }
.af-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}
.af-text {
  width: 100%;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: transparent;
  color: var(--text);
  font-size: 13px;
  padding: 8px 10px;
  resize: vertical;
}
.ask-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
.ab-btn {
  padding: 7px 14px;
  border-radius: 8px;
  font-size: 12.5px;
  cursor: pointer;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text);
}
.ab-btn.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #0b0c10;
  font-weight: 600;
}
.ab-btn.primary:disabled { opacity: 0.45; cursor: not-allowed; }
.ab-btn.danger { color: #f87171; border-color: rgba(248, 113, 113, 0.4); }
.ab-btn.ghost:hover { border-color: var(--accent); }
</style>
