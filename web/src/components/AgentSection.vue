<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '../lib/api'
import type { Agent } from '../types'
import AvatarBadge from './AvatarBadge.vue'
import Icon from './Icon.vue'
import { askConfirm } from '../lib/confirm'

const props = defineProps<{ agents: Agent[] }>()
const emit = defineEmits<{ changed: []; selected: [id: string] }>()

// 内置人格（小柯）不在管理列表出现：它始终存在，用户不该编辑或删除
const userAgents = computed(() => props.agents.filter((a) => !a.builtin))

const EMOJI_PRESETS = ['🤖', '🐱', '🦊', '🌸', '🔮', '🧠', '🐧', '⚡', '🌙', '💻', '🎭', '🔥']
const selectedId = ref('')
const editing = ref<Agent | null>(null) // 非空 = 编辑模式
const creating = ref(false)
const busy = ref(false)
const err = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const current = computed<Agent | null>(() => {
  if (creating.value) return editing.value
  return userAgents.value.find((a) => a.id === selectedId.value) || null
})

// 选中项变化或列表刷新时同步编辑副本（agents 异步加载，需把列表本身纳入监听）
watch(
  () => [selectedId.value, creating.value, userAgents.value] as const,
  () => {
    if (creating.value) return
    if (!selectedId.value && userAgents.value.length) {
      selectedId.value = userAgents.value[0].id // 默认选中第一个
    }
    const a = userAgents.value.find((x) => x.id === selectedId.value)
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
  const a = userAgents.value.find((x) => x.id === selectedId.value)
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
  const ok = await askConfirm({
    title: `删除智能体「${e.name}」`,
    message: '该智能体的历史会话记录不受影响。',
    confirmText: '删除',
  })
  if (!ok) return
  busy.value = true
  try {
    await api.deleteAgent(e.id)
    const rest = userAgents.value.filter((a) => a.id !== e.id)
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
        <button class="btn tiny primary" @click="startCreate">
          <Icon name="plus" :size="12" :stroke="2.2" />
          新建
        </button>
      </div>
      <div class="agent-list">
        <div
          v-for="a in userAgents"
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
          <span v-if="a.tools" class="tool-dot" title="可用工具"><Icon name="settings" :size="12" /></span>
        </div>
        <div v-if="!userAgents.length" class="empty-list">
          还没有自定义智能体。点「新建」创建一个，小柯作为内置人格始终可用。
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
      <div v-else class="empty-edit">选择左侧智能体开始编辑，或点「新建」。</div>
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
  gap: 12px;
  min-height: 0;
}

.col-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.col-title {
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-faint);
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
  background: rgba(255, 255, 255, 0.025);
  border: 1px solid var(--border);
  border-radius: 12px;
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background 0.15s ease,
    transform 0.15s var(--ease);
}

.agent-item:hover {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.045);
}

.agent-item.active {
  border-color: rgba(255, 154, 61, 0.22);
  background: linear-gradient(90deg, var(--accent-soft), rgba(255, 154, 61, 0.02));
  box-shadow: inset 3px 0 0 var(--accent);
}

.ai-meta {
  flex: 1;
  min-width: 0;
}

.ai-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ai-desc {
  font-size: 10.5px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-dot {
  display: flex;
  color: var(--text-faint);
}

.edit-col {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.03), rgba(255, 255, 255, 0.01));
  border: 1px solid var(--border);
  border-radius: 18px;
  padding: 22px 24px;
  box-shadow: var(--shadow-sm), var(--ring-inset);
}

.editor {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 640px;
}

.ed-head {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}

.ed-name {
  font-size: 17px;
  font-weight: 700;
  letter-spacing: -0.01em;
}

.ed-sub {
  margin-top: 2px;
  font-size: 11.5px;
  color: var(--text-faint);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field label {
  font-size: 11.5px;
  color: var(--text-muted);
}

.dim {
  color: var(--text-faint);
  font-weight: 400;
}

.inp {
  background: rgba(0, 0, 0, 0.26);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text);
  padding: 10px 12px;
  font-size: 12.5px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.inp:focus {
  outline: none;
  border-color: var(--border-accent);
  box-shadow: var(--focus-ring);
}

.area {
  resize: vertical;
  min-height: 150px;
  line-height: 1.7;
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
  width: 34px;
  height: 34px;
  border-radius: 9px;
  border: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.03);
  font-size: 17px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease, transform 0.12s ease;
}

.emoji-btn:hover {
  border-color: var(--border-accent);
  background: rgba(255, 255, 255, 0.055);
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
  font-size: 12px;
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
  font-size: 12px;
}

.ed-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

.spacer {
  flex: 1;
}

.empty-edit {
  color: var(--text-faint);
  font-size: 12.5px;
  padding: 48px 0;
  text-align: center;
}

.empty-list {
  padding: 14px 12px;
  border: 1px dashed var(--border);
  border-radius: 12px;
  color: var(--text-faint);
  font-size: 11.5px;
  line-height: 1.6;
}
</style>
