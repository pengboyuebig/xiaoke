<script setup lang="ts">
import { nextTick, onUnmounted, ref, watch } from 'vue'
import Icon from './Icon.vue'
import { confirmState, resolveConfirm } from '../lib/confirm'

const confirmBtn = ref<HTMLButtonElement | null>(null)

watch(
  () => confirmState.open,
  async (open) => {
    if (open) {
      // 打开后把焦点放到确认按钮，回车/空格可直接触发
      await nextTick()
      confirmBtn.value?.focus()
    }
  },
)

function onKeydown(e: KeyboardEvent) {
  if (!confirmState.open) return
  if (e.key === 'Escape') {
    e.preventDefault()
    resolveConfirm(false)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    resolveConfirm(true)
  }
}

// 弹窗打开时在 window 上监听按键：焦点可能在按钮上，事件不一定冒泡到遮罩
function onWindowKeydown(e: KeyboardEvent) {
  onKeydown(e)
}

watch(
  () => confirmState.open,
  (open) => {
    if (open) window.addEventListener('keydown', onWindowKeydown, true)
    else window.removeEventListener('keydown', onWindowKeydown, true)
  },
)

onUnmounted(() => {
  window.removeEventListener('keydown', onWindowKeydown, true)
})
</script>

<template>
  <div v-if="confirmState.open" class="cfm-mask" @click.self="resolveConfirm(false)">
    <div class="cfm-box" role="dialog" aria-modal="true" :aria-label="confirmState.title">
      <div class="cfm-icon" :class="{ danger: confirmState.danger }">
        <Icon :name="confirmState.danger ? 'warning' : 'circle-dot'" :size="18" />
      </div>
      <div class="cfm-main">
        <div class="cfm-body">
          <div class="cfm-title">{{ confirmState.title }}</div>
          <div v-if="confirmState.message" class="cfm-msg">{{ confirmState.message }}</div>
        </div>
        <div class="cfm-actions">
          <button class="cfm-btn ghost" @click="resolveConfirm(false)">
            {{ confirmState.cancelText }}
          </button>
          <button
            ref="confirmBtn"
            class="cfm-btn"
            :class="confirmState.danger ? 'danger' : 'primary'"
            @click="resolveConfirm(true)"
          >
            {{ confirmState.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cfm-mask {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(4, 5, 8, 0.74);
  backdrop-filter: blur(10px);
}

.cfm-box {
  width: min(440px, 100%);
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 20px 20px 16px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  background: linear-gradient(180deg, #1c202a 0%, #14171f 100%);
  box-shadow: var(--shadow);
}

.cfm-icon {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid var(--border-accent);
}

.cfm-icon.danger {
  color: var(--danger);
  background: var(--danger-soft);
  border-color: rgba(251, 113, 133, 0.28);
}

.cfm-main {
  flex: 1;
  min-width: 0;
}

.cfm-body {
  min-width: 0;
}

.cfm-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--text);
  line-height: 1.45;
  word-break: break-word;
}

.cfm-msg {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-muted);
  white-space: pre-line;
  word-break: break-word;
}

.cfm-actions {
  display: flex;
  gap: 8px;
  margin-top: 16px;
  justify-content: flex-end;
}

.cfm-btn {
  padding: 8px 16px;
  border-radius: 9px;
  border: 1px solid var(--border-strong);
  background: transparent;
  color: var(--text-muted);
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  transition:
    color var(--dur) var(--ease),
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease);
}

.cfm-btn:hover {
  color: var(--text);
  background: rgba(255, 255, 255, 0.05);
}

.cfm-btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.cfm-btn.primary {
  background: var(--accent-gradient);
  border-color: transparent;
  color: var(--accent-text);
  box-shadow: var(--shadow-accent);
}

.cfm-btn.danger {
  background: transparent;
  border-color: rgba(251, 113, 133, 0.35);
  color: var(--danger);
}

.cfm-btn.danger:hover {
  background: var(--danger-soft);
}

/* 动画只做位移，不改 opacity：
   窗口在后台时浏览器会节流动画，若动画卡在起始帧，opacity:0 会让弹窗
   永久不可见、连点都点不中。只做 transform 则最坏情况也只是位置偏 6px。 */
.cfm-box {
  animation: cfmPop 0.16s ease;
}

@keyframes cfmPop {
  from { transform: translateY(-6px) scale(0.98); }
  to { transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .cfm-box {
    animation: none;
  }
}

@media (max-width: 520px) {
  .cfm-box {
    flex-direction: column;
  }

  .cfm-actions {
    margin-top: 14px;
  }
}
</style>
