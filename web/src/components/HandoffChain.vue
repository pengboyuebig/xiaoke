<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import type { HandoffRecord } from '../types'

const props = defineProps<{ handoffs: HandoffRecord[] }>()

// 每个回合的调用链通常只有一两条，默认展开；多了才收起省空间
const expanded = ref(true)
const isImg = (v?: string) => !!v && (v.startsWith('/assets/') || v.startsWith('http'))

const maxDepth = computed(() => props.handoffs.reduce((m, h) => Math.max(m, h.depth), 0))

function dur(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

// 缩进按深度递进，让「谁调谁」的层级一眼能看出来
function indentStyle(depth: number) {
  return { marginLeft: `${Math.min(depth, 4) * 18}px` }
}
</script>

<template>
  <div class="handoff-chain">
    <button class="hc-head" type="button" @click="expanded = !expanded">
      <Icon name="share" :size="13" :stroke="2" />
      <span class="hc-title">智能体协作</span>
      <span class="hc-meta">{{ handoffs.length }} 次调用 · {{ maxDepth + 1 }} 层</span>
      <span class="hc-chevron" :class="{ up: expanded }">
        <Icon :name="expanded ? 'chevron-up' : 'chevron-down'" :size="13" />
      </span>
    </button>

    <div v-if="expanded" class="hc-body">
      <div
        v-for="(h, i) in handoffs"
        :key="i"
        class="hc-row"
        :class="{ failed: !h.ok }"
        :style="indentStyle(h.depth)"
      >
        <div class="hc-line">
          <span class="hc-from">{{ h.fromName }}</span>
          <Icon name="chevron-right" :size="12" :stroke="2.2" class="hc-arrow" />
          <span class="hc-to">{{ h.toName }}</span>
          <span class="hc-badge" :class="h.ok ? 'ok' : 'bad'">
            {{ h.ok ? '成功' : '失败' }}
          </span>
          <span class="hc-stats">
            {{ dur(h.ms) }}<template v-if="h.toolCalls"> · {{ h.toolCalls }} 次工具</template>
          </span>
        </div>

        <!-- 交给目标的任务说明 -->
        <details class="hc-detail">
          <summary>任务说明</summary>
          <pre class="hc-prompt">{{ h.prompt }}</pre>
        </details>

        <!-- 目标智能体的汇报 -->
        <details v-if="h.ok && h.report" class="hc-detail">
          <summary>汇报</summary>
          <pre class="hc-report">{{ h.report }}</pre>
        </details>

        <div v-if="!h.ok && h.error" class="hc-error">
          <Icon name="warning" :size="13" />
          <span>{{ h.error }}</span>
        </div>
      </div>

      <!-- 调用路径：把整条链画成一行，方便快速理解全局 -->
      <div v-if="handoffs.length > 1" class="hc-path">
        <span class="hc-path-label">调用链</span>
        <span class="hc-path-text">{{ handoffs.map((h) => `${h.fromName} → ${h.toName}`).join('，') }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.handoff-chain {
  margin: 4px 0 10px;
  border: 1px solid rgba(16, 185, 129, 0.22);
  border-radius: 10px;
  background: rgba(16, 185, 129, 0.04);
  overflow: hidden;
}

.hc-head {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  padding: 8px 12px;
  border: 0;
  background: transparent;
  color: var(--text, #3f3f3f);
  font-size: 12px;
  cursor: pointer;
  font-family: inherit;
}
.hc-head:hover {
  background: rgba(16, 185, 129, 0.08);
}

.hc-title {
  font-weight: 600;
}

.hc-meta {
  color: var(--text-muted, #8a8f99);
  font-size: 11px;
}

.hc-chevron {
  margin-left: auto;
  display: flex;
  color: var(--text-muted, #8a8f99);
  transition: transform 0.15s;
}
.hc-chevron.up {
  transform: rotate(180deg);
}

.hc-body {
  padding: 0 12px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.hc-row {
  border-left: 2px solid rgba(16, 185, 129, 0.35);
  padding-left: 10px;
}
.hc-row.failed {
  border-left-color: rgba(239, 68, 68, 0.5);
}

.hc-line {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12.5px;
}

.hc-from,
.hc-to {
  font-weight: 600;
  color: var(--text, #3f3f3f);
}

.hc-arrow {
  color: var(--text-muted, #8a8f99);
}

.hc-badge {
  font-size: 10.5px;
  padding: 1px 7px;
  border-radius: 999px;
  font-weight: 600;
}
.hc-badge.ok {
  background: rgba(16, 185, 129, 0.14);
  color: #047857;
}
.hc-badge.bad {
  background: rgba(239, 68, 68, 0.12);
  color: #b91c1c;
}

.hc-stats {
  margin-left: auto;
  color: var(--text-muted, #8a8f99);
  font-size: 11px;
}

.hc-detail {
  margin-top: 5px;
}
.hc-detail summary {
  font-size: 11.5px;
  color: var(--text-muted, #8a8f99);
  cursor: pointer;
  user-select: none;
}
.hc-detail summary:hover {
  color: var(--text, #3f3f3f);
}

.hc-prompt,
.hc-report {
  margin: 6px 0 0;
  padding: 8px 10px;
  background: rgba(0, 0, 0, 0.03);
  border-radius: 7px;
  font-size: 11.5px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  color: var(--text, #3f3f3f);
  max-height: 220px;
  overflow-y: auto;
}

.hc-report {
  background: rgba(16, 185, 129, 0.06);
}

.hc-error {
  margin-top: 6px;
  display: flex;
  gap: 6px;
  align-items: flex-start;
  font-size: 11.5px;
  color: #b91c1c;
  line-height: 1.6;
}

.hc-path {
  margin-top: 4px;
  padding-top: 10px;
  border-top: 1px dashed rgba(0, 0, 0, 0.08);
  font-size: 11.5px;
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.hc-path-label {
  color: var(--text-muted, #8a8f99);
  flex-shrink: 0;
}
.hc-path-text {
  color: var(--text, #3f3f3f);
  line-height: 1.7;
}
</style>
