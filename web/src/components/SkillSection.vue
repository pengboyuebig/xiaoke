<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../lib/api'
import type { Skill } from '../types'
import Icon from './Icon.vue'

const props = defineProps<{ skills: Skill[] }>()
const emit = defineEmits<{ changed: [] }>()

const selectedId = ref('')
const editing = ref<Skill | null>(null)
const creating = ref(false)
const busy = ref(false)
const err = ref('')

const current = computed<Skill | null>(() => {
  if (creating.value) return editing.value
  return props.skills.find((s) => s.id === selectedId.value) || null
})

// 选中项变化或列表刷新时同步编辑副本（skills 异步加载，需把列表本身纳入监听）
watch(
  () => [selectedId.value, creating.value, props.skills] as const,
  () => {
    if (creating.value) return
    if (!selectedId.value && props.skills.length) {
      selectedId.value = props.skills[0].id // 默认选中第一条
    }
    const s = props.skills.find((x) => x.id === selectedId.value)
    editing.value = s ? { ...s } : null
  },
  { immediate: true },
)

const enabledCount = computed(() => props.skills.filter((s) => s.enabled).length)

function select(s: Skill) {
  selectedId.value = s.id
  creating.value = false
}

function startCreate() {
  creating.value = true
  editing.value = {
    id: '',
    name: '',
    description: '',
    content: '',
    enabled: true,
    createdAt: '',
    updatedAt: '',
  }
  err.value = ''
}

function cancel() {
  creating.value = false
  const s = props.skills.find((x) => x.id === selectedId.value)
  editing.value = s ? { ...s } : null
  err.value = ''
}

async function save() {
  const e = editing.value
  if (!e || !e.name.trim() || !e.content.trim() || busy.value) return
  busy.value = true
  err.value = ''
  try {
    if (creating.value) {
      const created = await api.createSkill(e)
      selectedId.value = created.id
      creating.value = false
    } else {
      await api.updateSkill(e.id, e)
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
  if (!confirm(`删除技能「${e.name}」？`)) return
  busy.value = true
  try {
    await api.deleteSkill(e.id)
    const rest = props.skills.filter((s) => s.id !== e.id)
    selectedId.value = rest[0]?.id || ''
    emit('changed')
  } catch (ex) {
    err.value = (ex as Error).message
  } finally {
    busy.value = false
  }
}

async function toggle(s: Skill) {
  try {
    await api.toggleSkill(s.id)
    emit('changed')
  } catch (ex) {
    err.value = (ex as Error).message
  }
}

/** 启停开关：新建时改本地表单，已存在时调用接口 */
function onEnabledChange(e: Event) {
  const checked = (e.target as HTMLInputElement).checked
  const cur = editing.value
  if (!cur) return
  if (creating.value) {
    cur.enabled = checked
  } else {
    toggle(cur)
  }
}
</script>

<template>
  <div class="split">
    <!-- 左：技能列表（带启用开关） -->
    <div class="list-col">
      <div class="col-head">
        <span class="col-title">技能 <span class="count">{{ enabledCount }}/{{ props.skills.length }} 启用</span></span>
        <button class="btn tiny primary" @click="startCreate">
          <Icon name="plus" :size="12" :stroke="2.2" />
          新建
        </button>
      </div>
      <div class="skill-list">
        <div
          v-for="s in props.skills"
          :key="s.id"
          class="skill-item"
          :class="{ active: !creating && s.id === selectedId, off: !s.enabled }"
          @click="select(s)"
        >
          <div class="si-main">
            <div class="si-name">{{ s.name }}</div>
            <div class="si-desc">{{ s.description || '（无简介）' }}</div>
          </div>
          <button
            class="toggle"
            :class="{ on: s.enabled }"
            :title="s.enabled ? '点击停用' : '点击启用'"
            @click.stop="toggle(s)"
          >
            <span class="knob"></span>
          </button>
        </div>
        <div v-if="!props.skills.length" class="empty-list">还没有技能，点「新建」写一条。</div>
      </div>
      <div class="tip">启用的技能会注入小柯的系统提示词，随时生效。</div>
    </div>

    <!-- 右：编辑器 -->
    <div class="edit-col">
      <div v-if="current" class="editor">
        <div class="ed-head">
          <div class="ed-title">
            <div class="ed-name">{{ creating ? '新建技能' : current.name }}</div>
            <div class="ed-sub">Markdown 正文会作为「【技能：名字】」注入系统提示词</div>
          </div>
          <span class="spacer"></span>
          <label class="switch">
            <input type="checkbox" :checked="current.enabled" @change="onEnabledChange" />
            <span>{{ current.enabled ? '已启用' : '已停用' }}</span>
          </label>
        </div>

        <div class="row2">
          <div class="field">
            <label>技能名</label>
            <input v-model="current.name" class="inp" placeholder="如：金字塔写作" maxlength="16" />
          </div>
          <div class="field">
            <label>一句话简介</label>
            <input v-model="current.description" class="inp" placeholder="它解决什么问题" maxlength="40" />
          </div>
        </div>

        <div class="field grow">
          <label>内容（Markdown）</label>
          <textarea
            v-model="current.content"
            class="inp area"
            rows="12"
            placeholder="写清楚步骤、规则、示例……&#10;&#10;例如：&#10;1. 第一句话给出结论&#10;2. 论据按 MECE 分组"
          ></textarea>
        </div>

        <div v-if="err" class="err">{{ err }}</div>

        <div class="ed-foot">
          <button v-if="!creating" class="btn danger" :disabled="busy" @click="remove">删除</button>
          <span class="spacer"></span>
          <button v-if="creating" class="btn ghost" @click="cancel">取消</button>
          <button class="btn primary" :disabled="busy || !current.name.trim() || !current.content.trim()" @click="save">
            {{ busy ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
      <div v-else class="empty-edit">选择左侧技能开始编辑，或点「新建」。</div>
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
  width: 260px;
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

.count {
  font-size: 10px;
  color: var(--text-faint);
  font-weight: 400;
  margin-left: 4px;
}

.skill-list {
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-right: 4px;
}

.skill-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: rgba(255, 255, 255, 0.025);
  border: 1px solid var(--border);
  border-radius: 12px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.skill-item:hover {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.045);
}

.skill-item.active {
  border-color: rgba(255, 154, 61, 0.22);
  background: linear-gradient(90deg, var(--accent-soft), rgba(255, 154, 61, 0.02));
  box-shadow: inset 3px 0 0 var(--accent);
}

.skill-item.off .si-name {
  color: var(--text-faint);
  text-decoration: line-through;
}

.si-main {
  flex: 1;
  min-width: 0;
}

.si-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text);
}

.si-desc {
  font-size: 10.5px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 开关 */
.toggle {
  width: 34px;
  height: 19px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: rgba(255, 255, 255, 0.06);
  position: relative;
  cursor: pointer;
  flex-shrink: 0;
  transition: background 0.18s ease, border-color 0.18s ease;
  padding: 0;
}

.toggle.on {
  background: var(--accent-gradient);
  border-color: transparent;
  box-shadow: 0 0 14px rgba(255, 154, 61, 0.22);
}

.knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 13px;
  height: 13px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: transform 0.18s var(--ease);
}

.toggle.on .knob {
  transform: translateX(15px);
}

.tip {
  font-size: 10.5px;
  color: var(--text-faint);
  line-height: 1.55;
}

.empty-list {
  padding: 12px;
  color: var(--text-faint);
  font-size: 11.5px;
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
  max-width: 680px;
}

.ed-head {
  display: flex;
  align-items: center;
  gap: 12px;
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

.row2 {
  display: flex;
  gap: 14px;
}

.row2 .field {
  flex: 1;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field.grow {
  flex: 1;
}

.field label {
  font-size: 11.5px;
  color: var(--text-muted);
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
  min-height: 240px;
  line-height: 1.7;
  font-family: var(--font-mono);
  font-size: 12px;
}

.switch {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11.5px;
  color: var(--text-muted);
  cursor: pointer;
  flex-shrink: 0;
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
</style>
