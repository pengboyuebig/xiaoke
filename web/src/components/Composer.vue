<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{ busy: boolean; reasoning: string }>()
const emit = defineEmits<{ send: [content: string]; stop: []; 'update:reasoning': [v: string] }>()

const text = ref('')
const ta = ref<HTMLTextAreaElement | null>(null)

// 思考程度选项（none 不注入请求体；其余映射为 reasoning_effort）
const LEVELS = [
  { value: 'none', label: '关' },
  { value: 'minimal', label: '极简' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
]

// 输入框随内容自动增高（上限 5 行左右）
function resize() {
  const el = ta.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 150) + 'px'
}
watch(text, resize)

function submit() {
  const content = text.value.trim()
  if (!content || props.busy) return
  emit('send', content)
  text.value = ''
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    submit()
  }
}

function onLevelChange(e: Event) {
  emit('update:reasoning', (e.target as HTMLSelectElement).value)
}
</script>

<template>
  <div class="composer-wrap">
    <div class="composer">
      <textarea
        ref="ta"
        v-model="text"
        rows="1"
        placeholder="问问小柯…（Enter 发送，Shift+Enter 换行）"
        @keydown="onKeydown"
      ></textarea>
      <button v-if="props.busy" class="btn stop" title="停止生成" @click="emit('stop')">
        <span class="stop-icon"></span>
      </button>
      <button v-else class="btn send" title="发送" :disabled="!text.trim()" @click="submit">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 19V5M5 12l7-7 7 7" />
        </svg>
      </button>
    </div>
    <div class="hint-row">
      <span class="hint">小柯可能也会犯错，重要信息请自行核实。</span>
      <!-- 右下角：思考程度（当前会话的下一条消息生效，浏览器本地记住） -->
      <label class="level" title="模型思考程度（reasoning_effort），下一条消息生效">
        <span class="level-icon">🧠</span>
        <select :value="props.reasoning" @change="onLevelChange">
          <option v-for="l in LEVELS" :key="l.value" :value="l.value">{{ l.label }}</option>
        </select>
      </label>
    </div>
  </div>
</template>

<style scoped>
.composer-wrap {
  max-width: var(--content-w);
  width: 100%;
  margin: 0 auto;
  padding: 0 24px 14px;
}

.composer {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 10px 10px 10px 16px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.composer:focus-within {
  border-color: var(--accent);
  box-shadow: var(--focus-ring);
}

textarea {
  flex: 1;
  background: none;
  border: none;
  outline: none;
  resize: none;
  color: var(--text);
  line-height: 1.55;
  padding: 4px 0;
  max-height: 150px;
}

textarea::placeholder {
  color: var(--text-faint);
}

.btn {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s ease, transform 0.1s ease;
}

.btn.send {
  background: var(--accent);
  color: var(--accent-text);
}

.btn.send:hover:not(:disabled) {
  background: var(--accent-hover);
}

.btn.send:active {
  transform: scale(0.94);
}

.btn.send:disabled {
  background: var(--bg-surface-2);
  color: var(--text-faint);
  cursor: not-allowed;
}

.btn.stop {
  background: var(--bg-surface-2);
  border: 1px solid var(--border-strong);
}

.btn.stop:hover {
  border-color: var(--danger);
}

.stop-icon {
  width: 11px;
  height: 11px;
  background: var(--danger);
  border-radius: 2px;
}

/* 底部行：左侧免责提示，右侧思考程度选择器 */
.hint-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 8px;
}

.hint {
  color: var(--text-faint);
  font-size: 12px;
}

.level {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--text-muted);
  flex-shrink: 0;
  cursor: pointer;
}

.level-icon {
  font-size: 13px;
}

.level select {
  background: var(--bg-surface);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 3px 10px;
  font-size: 12px;
  cursor: pointer;
  outline: none;
}

.level select:hover {
  border-color: var(--accent);
}

.level select:focus {
  border-color: var(--accent);
  box-shadow: var(--focus-ring);
}
</style>
