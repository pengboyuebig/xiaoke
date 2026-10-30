<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'

const props = defineProps<{ busy: boolean; reasoning: string; mode?: string }>()
const emit = defineEmits<{
  send: [content: string]
  stop: []
  'update:reasoning': [v: string]
  setMode: [m: 'plan' | 'build']
}>()

const text = ref('')
const ta = ref<HTMLTextAreaElement | null>(null)

// 输入框左侧的工作模式切换
const modeMenuOpen = ref(false)
const modeWrap = ref<HTMLElement | null>(null)
const isPlan = () => props.mode === 'plan'
function pickMode(m: 'plan' | 'build') {
  emit('setMode', m)
  modeMenuOpen.value = false
}

// 点击面板外部关闭
function onDocClick(e: MouseEvent) {
  if (!modeMenuOpen.value) return
  if (modeWrap.value && !modeWrap.value.contains(e.target as Node)) modeMenuOpen.value = false
}
onMounted(() => document.addEventListener('mousedown', onDocClick))
onUnmounted(() => document.removeEventListener('mousedown', onDocClick))

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
      <!-- 输入框左侧：工作模式切换（计划 ⇄ 开发） -->
      <div ref="modeWrap" class="mode-wrap">
        <div v-if="modeMenuOpen" class="mode-menu">
          <button class="mode-opt" :class="{ active: isPlan() }" @click="pickMode('plan')">
            <span class="mo-icon">📋</span>
            <span class="mo-text">
              <b>计划模式</b>
              <i>先调研出计划，只读不改，确认后才开发</i>
            </span>
            <span v-if="isPlan()" class="mo-check">✓</span>
          </button>
          <button class="mode-opt" :class="{ active: !isPlan() }" @click="pickMode('build')">
            <span class="mo-icon">🛠️</span>
            <span class="mo-text">
              <b>开发模式</b>
              <i>完整能力：读写文件、执行命令</i>
            </span>
            <span v-if="!isPlan()" class="mo-check">✓</span>
          </button>
        </div>
        <button
          class="mode-btn"
          :class="isPlan() ? 'plan' : 'build'"
          :title="isPlan() ? '计划模式：先调研再开发（点击切换）' : '开发模式：完整能力（点击切换）'"
          @click="modeMenuOpen = !modeMenuOpen"
        >
          <span class="mb-icon">{{ isPlan() ? '📋' : '🛠️' }}</span>
          <span class="mb-label">{{ isPlan() ? '计划' : '开发' }}</span>
        </button>
      </div>

      <textarea
        ref="ta"
        v-model="text"
        rows="1"
        :placeholder="props.mode === 'plan'
          ? '计划模式：告诉小柯要调研什么，先出计划再开发…（Enter 发送）'
          : '问问小柯…（Enter 发送，Shift+Enter 换行）'"
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

/* ---------- 输入框左侧：工作模式 ---------- */
.mode-wrap {
  position: relative;
  flex-shrink: 0;
  align-self: flex-end;
}

.mode-btn {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 7px 11px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-surface-2);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.15s ease;
}

.mode-btn:hover {
  border-color: var(--border-strong);
  color: var(--text);
}

.mode-btn.plan {
  border-color: color-mix(in srgb, #56c2e6 45%, transparent);
  background: rgba(86, 194, 230, 0.1);
  color: #56c2e6;
}

.mode-btn.build {
  border-color: color-mix(in srgb, var(--accent) 40%, transparent);
  background: var(--accent-soft);
  color: var(--accent);
}

.mb-icon {
  font-size: 13px;
}

.mb-label {
  font-weight: 600;
}

.mode-menu {
  position: absolute;
  bottom: calc(100% + 10px);
  left: 0;
  width: 268px;
  background: var(--bg-surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  z-index: 30;
}

.mode-opt {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 9px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s ease;
}

.mode-opt:hover {
  background: var(--bg-surface-2);
}

.mode-opt.active {
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--accent) 30%, transparent);
}

.mo-icon {
  font-size: 15px;
  margin-top: 1px;
}

.mo-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.mo-text b {
  font-size: 13px;
  color: var(--text);
}

.mo-text i {
  font-style: normal;
  font-size: 11px;
  color: var(--text-faint);
  line-height: 1.45;
  margin-top: 2px;
}

.mo-check {
  color: var(--accent);
  font-size: 13px;
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
