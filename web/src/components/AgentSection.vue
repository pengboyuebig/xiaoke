<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '../lib/api'
import type { Agent } from '../types'
import AvatarBadge from './AvatarBadge.vue'

const props = defineProps<{ agents: Agent[] }>()
const emit = defineEmits<{ changed: []; selected: [id: string] }>()

const EMOJI_PRESETS = ['🤖', '🐱', '🦊', '🌸', '🔮', '🧠', '🐧', '⚡', '🌙', '💻', '🎭', '🔥']
const selectedId = ref('')
const editing = ref<Agent | null>(null) // 非空 = 编辑模式
const creating = ref(false)
const busy = ref(false)
const err = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const current = computed<Agent | null>(() => {
  if (creating.value) return editing.value
  return props.agents.find((a) => a.id === selectedId.value) || null
})

// 选中项变化或列表刷新时同步编辑副本（agents 异步加载，需把列表本身纳入监听）
watch(
  () => [selectedId.value, creating.value, props.agents] as const,
  () => {
    if (creating.value) return
    if (!selectedId.value && props.agents.length) {
      selectedId.value = props.agents[0].id // 默认选中第一个
    }
    const a = props.agents.find((x) => x.id === selectedId.value)
    editing.value = a ? { ...a } : null
  },
  { immediate: true, deep: false },
)

function select(a: Agent) {
  selectedId.value = a.id
  creating.value = false
  emit('selected', a.id)
}

function startCreate() {
  creating.value = true
  editing.value = {
    id: '',
    name: '',
    description: '',
    avatar: '🤖',
    systemPrompt: '',
    tools: true,
    createdAt: '',
    updatedAt: '',
  }
  err.value = ''
}

function cancel() {
  creating.value = false
  const a = props.agents.find((x) => x.id === selectedId.value)
  editing.value = a ? { ...a } : null
  err.value = ''
}

async function save() {
  const e = editing.value
  if (!e || !e.name.trim() || busy.value) return
  busy.value = true
  err.value = ''
  try {
    if (creating.value) {
      const created = await api.createAgent(e)
      selectedId.value = created.id
      creating.value = false
    } else {
      await api.updateAgent(e.id, e)
    }
    emit('changed')
  } catch (ex) {
    err.value = (ex as Error).message
  } finally {
    busy.value = false
  }
}

async function remove() {
  const e = editing.value
  if (!e || creating.value || busy.value) return
  if (!confirm(`删除智能体「${e.name}」？（会话记录不受影响）`)) return
  busy.value = true
  try {
    await api.deleteAgent(e.id)
    const rest = props.agents.filter((a) => a.id !== e.id)
    selectedId.value = rest[0]?.id || ''
    emit('changed')
  } catch (ex) {
    err.value = (ex as Error).message
  } finally {
    busy.value = false
  }
}

async function onPickFile(ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !editing.value) return
  err.value = ''
  try {
    const { url } = await api.uploadAvatar(file)
    editing.value.avatar = url
  } catch (ex) {
    err.value = (ex as Error).message
  }
}
</script>

<template>
  <div class="split">
    <!-- 左：智能体列表 -->
    <div class="list-col">
      <div class="col-head">
        <span class="col-title">智能体</span>
        <button class="btn tiny primary" @click="startCreate">＋ 新建</button>
      </div>
      <div class="agent-list">
        <div
          v-for="a in props.agents"
          :key="a.id"
          class="agent-item"
          :class="{ active: !creating && a.id === selectedId }"
          @click="select(a)"
        >
          <AvatarBadge :value="a.avatar" :fallback="a.name" :size="36" />
          <div class="ai-meta">
            <div class="ai-name">{{ a.name }}</div>
            <div class="ai-desc">{{ a.description || (a.tools ? '可用工具' : '纯对话') }}</div>
          </div>
          <span v-if="a.tools" class="tool-dot" title="可用工具">⚙</span>
        </div>
      </div>
    </div>

    <!-- 右：编辑器 -->
    <div class="edit-col">
      <div v-if="current" class="editor">
        <div class="ed-head">
          <AvatarBadge :value="current.avatar" :fallback="current.name" :size="52" />
          <div class="ed-title">
            <div class="ed-name">{{ creating ? '新建智能体' : current.name }}</div>
            <div class="ed-sub">{{ creating ? '定义一个有个性的新角色' : '修改后立即生效' }}</div>
          </div>
        </div>

        <div class="field">
          <label>名字</label>
          <input v-model="current.name" class="inp" placeholder="如：写作助手 / 代码评审员" maxlength="16" />
        </div>
        <div class="field">
          <label>一句话简介</label>
          <input v-model="current.description" class="inp" placeholder="它擅长什么" maxlength="40" />
        </div>

        <div class="field">
          <label>头像</label>
          <div class="avatar-row">
            <AvatarBadge :value="current.avatar" :fallback="current.name" :size="44" />
            <div class="emoji-grid">
              <button
                v-for="e in EMOJI_PRESETS"
                :key="e"
                class="emoji-btn"
                :class="{ active: current.avatar === e }"
                @click="current.avatar = e"
              >{{ e }}</button>
            </div>
            <button class="btn ghost tiny" @click="fileInput?.click()">上传图片</button>
            <input ref="fileInput" type="file" accept="image/*" hidden @change="onPickFile" />
          </div>
        </div>

        <div class="field">
          <label>人格提示词 <span class="dim">（留空 = 使用默认小柯人设；支持 Markdown）</span></label>
          <textarea
            v-model="current.systemPrompt"
            class="inp area"
            rows="7"
            placeholder="描述它的性格、说话方式、专长……"
          ></textarea>
        </div>

        <label class="switch">
          <input type="checkbox" v-model="current.tools" />
          <span>允许使用工具（读写文件 / 执行命令 / MCP）</span>
        </label>

        <div v-if="err" class="err">{{ err }}</div>

        <div class="ed-foot">
          <button v-if="!creating" class="btn danger" :disabled="busy" @click="remove">删除</button>
          <span class="spacer"></span>
          <button v-if="creating" class="btn ghost" @click="cancel">取消</button>
          <button class="btn primary" :disabled="busy || !current.name.trim()" @click="save">
            {{ busy ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
      <div v-else class="empty-edit">选择左侧智能体开始编辑，或点「＋ 新建」。</div>
    </div>
  </div>
</template>

<style scoped>
.split {
  display: flex;
  gap: 20px;
  min-height: 0;
  flex: 1;
}

.list-col {
  width: 250px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 0;
}

.col-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.col-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
}

.agent-list {
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-right: 4px;
}

.agent-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.agent-item:hover {
  border-color: var(--border-strong);
  background: var(--bg-surface-2);
}

.agent-item.active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.ai-meta {
  flex: 1;
  min-width: 0;
}

.ai-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ai-desc {
  font-size: 11px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-dot {
  color: var(--accent);
  font-size: 12px;
}

.edit-col {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 20px 22px;
}

.editor {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 640px;
}

.ed-head {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}

.ed-name {
  font-size: 16px;
  font-weight: 700;
}

.ed-sub {
  font-size: 12px;
  color: var(--text-faint);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field label {
  font-size: 12px;
  color: var(--text-muted);
}

.dim {
  color: var(--text-faint);
  font-weight: 400;
}

.inp {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text);
  padding: 9px 12px;
  font-size: 13px;
}

.inp:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--focus-ring);
}

.area {
  resize: vertical;
  min-height: 130px;
  line-height: 1.6;
  font-family: inherit;
}

.avatar-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.emoji-grid {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.emoji-btn {
  width: 32px;
  height: 32px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--bg-surface);
  font-size: 16px;
  cursor: pointer;
  transition: border-color 0.12s ease, transform 0.1s ease;
}

.emoji-btn:hover {
  border-color: var(--accent);
  transform: translateY(-1px);
}

.emoji-btn.active {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.switch {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-muted);
  cursor: pointer;
}

.switch input {
  accent-color: var(--accent);
  width: 15px;
  height: 15px;
}

.err {
  color: var(--danger);
  font-size: 13px;
}

.ed-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}

.spacer {
  flex: 1;
}

.empty-edit {
  color: var(--text-faint);
  font-size: 13px;
  padding: 40px 0;
  text-align: center;
}
</style>
