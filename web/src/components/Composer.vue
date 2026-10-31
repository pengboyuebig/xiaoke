<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import Icon from './Icon.vue'

const emit = defineEmits<{
  send: [content: string]
  stop: []
  'update:reasoning': [v: string]
  setMode: [m: 'plan' | 'build' | 'ask']
}>()

const text = ref('')
const ta = ref<HTMLTextAreaElement | null>(null)

// 草稿按会话保存：切会话/误刷新不丢输入。上限 20 条草稿防 localStorage 膨胀。
const DRAFT_PREFIX = 'xiaoke-draft:'
const DRAFT_MAX = 20
const props = defineProps<{ busy: boolean; reasoning: string; mode?: string; sessionId?: string }>()

function loadDraft(sid?: string) {
  if (!sid) return ''
  try {
    return localStorage.getItem(DRAFT_PREFIX + sid) || ''
  } catch {
    return ''
  }
}
function saveDraft(sid: string | undefined, value: string) {
  if (!sid) return
  try {
    if (value) localStorage.setItem(DRAFT_PREFIX + sid, value)
    else localStorage.removeItem(DRAFT_PREFIX + sid)
    // 粗略防膨胀：草稿键过多时按 key 序清理一批（非精确 LRU，够用即可）
    const keys: string[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)
      if (k?.startsWith(DRAFT_PREFIX)) keys.push(k)
    }
    if (keys.length > DRAFT_MAX) {
      keys.sort()
      for (const k of keys.slice(0, keys.length - DRAFT_MAX)) localStorage.removeItem(k)
    }
  } catch {
    /* 存储满/隐私模式：忽略 */
  }
}
watch(
  () => props.sessionId,
  (sid, prev) => {
    if (prev && text.value.trim()) saveDraft(prev, text.value)
    text.value = loadDraft(sid)
  },
  { immediate: true },
)
watch(text, (v) => saveDraft(props.sessionId, v))

// 输入框左侧的工作模式切换
const modeMenuOpen = ref(false)
const modeWrap = ref<HTMLElement | null>(null)
const isPlan = () => props.mode === 'plan'
const isAsk = () => props.mode === 'ask'
function pickMode(m: 'plan' | 'build' | 'ask') {
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
  saveDraft(props.sessionId, '')
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
            <span class="mo-icon"><Icon name="plan" :size="16" /></span>
            <span class="mo-text">
              <b>计划模式</b>
              <i>先调研出计划，只读不改，确认后才开发</i>
            </span>
            <span v-if="isPlan()" class="mo-check"><Icon name="check" :size="13" /></span>
          </button>
          <button class="mode-opt" :class="{ active: isAsk() }" @click="pickMode('ask')">
            <span class="mo-icon"><Icon name="lock" :size="16" /></span>
            <span class="mo-text">
              <b>询问模式</b>
              <i>每一步写操作都先征求你的批准</i>
            </span>
            <span v-if="isAsk()" class="mo-check"><Icon name="check" :size="13" /></span>
          </button>
          <button class="mode-opt" :class="{ active: !isPlan() && !isAsk() }" @click="pickMode('build')">
            <span class="mo-icon"><Icon name="build" :size="16" /></span>
            <span class="mo-text">
              <b>开发模式</b>
              <i>完整能力：读写文件、执行命令</i>
            </span>
            <span v-if="!isPlan()" class="mo-check"><Icon name="check" :size="13" /></span>
          </button>
        </div>
        <button
          class="mode-btn"
          :class="isPlan() ? 'plan' : isAsk() ? 'plan' : 'build'"
          :title="isPlan() ? '计划模式：先调研再开发（点击切换）' : isAsk() ? '询问模式：每步写操作先征批（点击切换）' : '开发模式：完整能力（点击切换）'"
          @click="modeMenuOpen = !modeMenuOpen"
        >
          <span class="mb-icon"><Icon :name="isPlan() ? 'plan' : isAsk() ? 'lock' : 'build'" :size="15" /></span>
          <span class="mb-label">{{ isPlan() ? '计划' : isAsk() ? '询问' : '开发' }}</span>
        </button>
      </div>

      <textarea
        ref="ta"
        v-model="text"
        rows="1"
        :placeholder="props.mode === 'plan'
          ? '计划模式：告诉小柯要调研什么，先出计划再开发…（Enter 发送）'
          : props.mode === 'ask'
          ? '询问模式：每步写操作都会先征求你的批准…（Enter 发送）'
          : '问问小柯…（Enter 发送，Shift+Enter 换行）'"
        @keydown="onKeydown"
      ></textarea>
      <button v-if="props.busy" class="btn stop" title="停止生成" @click="emit('stop')">
        <span class="stop-icon"></span>
      </button>
      <button v-else class="btn send" title="发送" :disabled="!text.trim()" @click="submit">
        <Icon name="arrow-up" :size="17" :stroke="2.2" />
      </button>
    </div>
    <div class="hint-row">
      <span class="hint">小柯可能也会犯错，重要信息请自行核实。</span>
      <!-- 右下角：思考程度（当前会话的下一条消息生效，浏览器本地记住） -->
      <label class="level" title="模型思考程度（reasoning_effort），下一条消息生效">
        <span class="level-icon"><Icon name="brain" :size="14" /></span>
        <select :value="props.reasoning" @change="onLevelChange">
          <option v-for="l in LEVELS" :key="l.value" :value="l.value">{{ l.label }}</option>
        </select>
      </label>
    </div>
  </div>
</template>

<style scoped>
.composer-wrap {
  position: relative;
  max-width: var(--content-w);
  width: 100%;
  margin: 0 auto;
  padding: 0 28px 16px;
}

.composer-wrap::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -48px;
  height: 48px;
  background: linear-gradient(180deg, transparent, rgba(10, 11, 15, 0.72));
  pointer-events: none;
}

.composer {
  position: relative;
  display: flex;
  align-items: flex-end;
  gap: 7px;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.055), rgba(255, 255, 255, 0.022));
  border: 1px solid var(--border);
  border-radius: 20px;
  padding: 9px;
  box-shadow: var(--shadow-soft), var(--ring-inset);
  backdrop-filter: blur(20px);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.composer:focus-within {
  border-color: rgba(255, 154, 61, 0.35);
  box-shadow:
    0 0 0 3px rgba(255, 154, 61, 0.09),
    0 18px 44px rgba(0, 0, 0, 0.35),
    var(--ring-inset);
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
  gap: 6px;
  padding: 8px 11px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.045);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  transition:
    border-color 0.15s ease,
    background 0.15s ease,
    color 0.15s ease;
}

.mode-btn:hover {
  border-color: var(--border-strong);
  color: var(--text);
  background: rgba(255, 255, 255, 0.07);
}

.mode-btn.plan {
  border-color: rgba(103, 232, 249, 0.28);
  background: rgba(103, 232, 249, 0.09);
  color: var(--cyan);
}

.mode-btn.build {
  border-color: rgba(255, 154, 61, 0.26);
  background: var(--accent-soft);
  color: var(--accent);
}

.mb-icon {
  display: flex;
}

.mb-label {
  font-weight: 600;
  letter-spacing: 0.02em;
}

.mode-menu {
  position: absolute;
  bottom: calc(100% + 10px);
  left: 0;
  width: 290px;
  background: linear-gradient(180deg, #1c202a 0%, #14171f 100%);
  border: 1px solid var(--border-strong);
  border-radius: 16px;
  box-shadow: var(--shadow);
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  z-index: 30;
}

.mode-opt {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px;
  border-radius: 11px;
  border: 1px solid transparent;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.mode-opt:hover {
  background: rgba(255, 255, 255, 0.055);
}

.mode-opt.active {
  background: linear-gradient(135deg, rgba(255, 154, 61, 0.12), rgba(255, 154, 61, 0.04));
  border-color: rgba(255, 154, 61, 0.18);
}

.mo-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.05);
  color: var(--text-muted);
  flex-shrink: 0;
}

.mode-opt.active .mo-icon {
  background: var(--accent-soft);
  color: var(--accent);
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
  display: flex;
  color: var(--accent);
  margin-top: 5px;
}

textarea {
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  outline: none;
  resize: none;
  color: var(--text);
  font-size: 14px;
  line-height: 1.6;
  padding: 8px 4px 7px;
  min-height: 36px;
  max-height: 160px;
}

textarea::placeholder {
  color: var(--text-faint);
}

.btn {
  flex-shrink: 0;
  width: 38px;
  height: 38px;
  border-radius: 13px;
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: transform 0.12s ease, filter 0.15s ease, background 0.15s ease;
}

.btn.send {
  background: var(--accent-gradient);
  color: var(--accent-text);
  box-shadow: var(--shadow-accent), inset 0 1px 0 rgba(255, 255, 255, 0.35);
}

.btn.send:hover:not(:disabled) {
  filter: brightness(1.05);
  transform: translateY(-1px);
}

.btn.send:active {
  transform: scale(0.95);
}

.btn.send:disabled {
  background: rgba(255, 255, 255, 0.05);
  color: var(--text-faint);
  box-shadow: none;
  cursor: not-allowed;
}

.btn.stop {
  background: rgba(251, 113, 133, 0.1);
  border: 1px solid rgba(251, 113, 133, 0.25);
}

.btn.stop:hover {
  background: rgba(251, 113, 133, 0.16);
  border-color: rgba(251, 113, 133, 0.45);
}

.stop-icon {
  width: 10px;
  height: 10px;
  background: var(--danger);
  border-radius: 2px;
}

/* 底部行：左侧免责提示，右侧思考程度选择器 */
.hint-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 9px;
  padding: 0 4px;
}

.hint {
  color: var(--text-faint);
  font-size: 10.5px;
}

.level {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--text-muted);
  flex-shrink: 0;
  cursor: pointer;
}

.level-icon {
  display: flex;
  color: var(--text-faint);
}

.level select {
  appearance: none;
  -webkit-appearance: none;
  background-color: rgba(255, 255, 255, 0.04);
  background-image:
    linear-gradient(45deg, transparent 50%, var(--text-faint) 50%),
    linear-gradient(135deg, var(--text-faint) 50%, transparent 50%);
  background-position:
    calc(100% - 12px) 50%,
    calc(100% - 8px) 50%;
  background-size: 4px 4px, 4px 4px;
  background-repeat: no-repeat;
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 4px 24px 4px 10px;
  font-size: 11px;
  cursor: pointer;
  outline: none;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.level select:hover {
  border-color: var(--border-strong);
}

.level select:focus {
  border-color: var(--border-accent);
  box-shadow: var(--focus-ring);
}

@media (max-width: 760px) {
  .composer-wrap {
    padding: 0 14px 12px;
  }

  .mode-btn {
    padding: 8px 10px;
  }

  .mb-label {
    display: none;
  }

  .hint {
    display: none;
  }

  .hint-row {
    justify-content: flex-end;
  }
}
</style>
