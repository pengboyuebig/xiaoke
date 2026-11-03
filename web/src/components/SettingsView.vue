<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch, watchEffect } from 'vue'
import { api } from '../lib/api'
import type { Agent, Automation, CustomCommand, LLMProfilePayload, MCPServerCfg, MCPStatus, PermissionRule, SettingsView, Skill, TrashItem } from '../types'
import type { VersionInfo } from '../lib/api'
import { renderMarkdown } from '../lib/markdown'

/** 档案的本地编辑态：apiKey 输入框值（空=保持）+ 脱敏信息 + 每档案的拉取/测试状态 */
interface ProfileEdit extends LLMProfilePayload {
  apiKeySet: boolean
  apiKeyTail: string
}
import AgentSection from './AgentSection.vue'
import SkillSection from './SkillSection.vue'
import Icon from './Icon.vue'
import { askConfirm } from '../lib/confirm'

const emit = defineEmits<{ close: [] }>()

type Tab = 'agents' | 'skills' | 'llm' | 'mcp' | 'dirs' | 'adv' | 'about'
const tab = ref<Tab>('llm')
const loading = ref(true)
const saving = ref(false)
const saveErr = ref('')
const toast = ref('')

const TABS: { id: Tab; icon: string; label: string; desc: string }[] = [
  { id: 'agents', icon: 'bot', label: '智能体', desc: '名字、头像、人格' },
  { id: 'skills', icon: 'book', label: '技能', desc: '可复用的知识注入' },
  { id: 'llm', icon: 'brain', label: '模型', desc: 'API 与思考程度' },
  { id: 'mcp', icon: 'plug', label: 'MCP', desc: '外部工具服务' },
  { id: 'dirs', icon: 'folder', label: '目录', desc: '只读授权与项目根' },
  { id: 'adv', icon: 'settings', label: '高级', desc: '轮数与命令白名单' },
  { id: 'about', icon: 'circle-dot', label: '关于', desc: '版本与更新说明' },
]

// ---------- 数据 ----------
const agents = ref<Agent[]>([])
const skills = ref<Skill[]>([])
const mcpStatuses = ref<MCPStatus[]>([])
const trashItems = ref<TrashItem[]>([])
const automations = ref<Automation[]>([])
const autoBusy = ref('')
const newAuto = reactive({ title: '', cron: '', prompt: '' })
const trashBusy = ref('')

const form = reactive({
  mode: 'mock',
  baseURL: '',
  apiKey: '',
  model: '',
  temperature: 0.7,
  maxHistory: 20,
  readDirs: [] as string[],
  newReadDir: '',
  projectsRoot: '',
  maxToolSteps: 20,
  maxTotalSteps: 120,
  allowlistText: 'git, npm, node, python, pip',
  maxTokens: 0,
  contextWindow: 0,
  smallModel: '',
  autoCompact: true,
  commandTimeoutSec: 0,
  memoryText: '',
  memoryDirty: false,
  permissions: [] as PermissionRule[],
  commands: [] as CustomCommand[],
  profiles: [] as ProfileEdit[],
  mcpServers: [] as (MCPServerCfg & { argLines: string })[],
})
const newMcp = reactive({ name: '', command: '', argsText: '', dir: '' })
// 档案编辑态
const expandedProfile = ref('')
const activeProfile = ref('')
const profileTest = ref<Record<string, { ok: boolean; text: string }>>({})
const profileModels = ref<Record<string, string[]>>({})
const profileBusy = ref('')
const profileSaving = ref('')
const customLLM = ref(false) // 当前生效值与激活档案不一致（手动自定义过）
/** 档案区串行队列：所有「改档案 + 保存 + 刷新」排队执行——
 *  并发 PUT 会乱序覆盖（后到的旧快照把新档案抹掉），loadSettings 的旧快照
 *  也会把刚 push 的卡片抹掉，必须全部串行。 */
let saveQueue: Promise<void> = Promise.resolve()
function enqueue(task: () => Promise<void>): Promise<void> {
  saveQueue = saveQueue.then(task, task)
  return saveQueue
}

/** 无队列的保存本体：PUT + 激活同步 + 重载视图 */
async function doSaveProfiles(syncActiveId?: string) {
  await api.saveSettings(buildPayload())
  if (syncActiveId && activeProfile.value === syncActiveId) {
    await api.activateModel(syncActiveId)
  }
  await loadSettings()
}

const adoptBusy = ref(false)
const adoptOpen = ref(false)
const legacyBackend = ref(false)
const adoptName = ref('')
const shortModelName = computed(() => (form.model || '').split('/').pop() || '')

/** 未绑定档案：起名收编当前生效配置（插入列表第一位，激活） */
function confirmAdopt() {
  adoptBusy.value = true
  return enqueue(async () => {
    await api.adoptModel({ name: adoptName.value.trim() || shortModelName.value })
    adoptOpen.value = false
    await loadSettings()
    showToast('已保存为档案 ✓')
  }).finally(() => {
    adoptBusy.value = false
  })
}

/** 生效值漂移：写回激活档案 */
function adoptCurrent() {
  adoptBusy.value = true
  return enqueue(async () => {
    await api.adoptModel({ id: activeProfile.value })
    await loadSettings()
    showToast('已同步到档案 ✓')
  }).finally(() => {
    adoptBusy.value = false
  })
}

function newProfileID(): string {
  return 'p' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

function addProfile() {
  if (form.profiles.length >= 20) return
  enqueue(async () => {
    // 预填名字让新卡片立刻成为真实档案（立即落库：刷新不丢、可直接点「使用」）。
    // push 必须在队列内做：并发的 loadSettings 会把刚 push 的卡片抹掉。
    let n = form.profiles.length + 1
    const taken = new Set(form.profiles.map((x) => x.name))
    let name = `新配置 ${n}`
    while (taken.has(name)) name = `新配置 ${++n}`
    const p: ProfileEdit = { id: newProfileID(), name, mode: 'openai', baseURL: '', apiKey: '', apiKeySet: false, apiKeyTail: '', model: '', smallModel: '' }
    form.profiles.push(p)
    expandedProfile.value = p.id
    await doSaveProfiles()
  })
}

function removeProfile(i: number) {
  const p = form.profiles[i]
  return askConfirm({
    title: `删除模型配置「${p?.name || '未命名'}」`,
    message: p?.apiKeySet ? '该档案保存的 API Key 将一并删除，无法恢复。确定继续？' : '确定删除这个配置档案？',
    confirmText: '删除',
  }).then((ok) => {
    if (!ok) return
    if (expandedProfile.value === p?.id) expandedProfile.value = ''
    return enqueue(async () => {
      const idx = form.profiles.findIndex((x) => x.id === p?.id)
      if (idx >= 0) form.profiles.splice(idx, 1)
      await doSaveProfiles()
    })
  })
}

function activateNow(id: string) {
  profileBusy.value = id
  return enqueue(async () => {
    await api.activateModel(id)
    await loadSettings()
    const name = form.profiles.find((p) => p.id === id)?.name || id
    showToast(`已切换到「${name}」✓`)
  }).finally(() => {
    profileBusy.value = ''
  })
}

/** 显式保存单个档案：不收起卡片，方便改完继续调；编辑激活档案时同步生效值 */
async function saveProfile(p: ProfileEdit) {
  profileSaving.value = p.id
  try {
    await saveProfiles(p.id === activeProfile.value ? p.id : undefined)
    showToast('已保存 ✓')
  } catch (e) {
    showToast(`保存失败：${(e as Error).message}`)
  } finally {
    profileSaving.value = ''
  }
}

async function toggleProfileEdit(p: ProfileEdit) {
  if (expandedProfile.value === p.id) {
    expandedProfile.value = ''
    // 收起即保存；编辑的是激活档案 → 把档案值同步进生效配置（改激活档案 = 改当前配置）
    const wasActive = p.id === activeProfile.value
    await enqueue(() => doSaveProfiles(wasActive ? p.id : undefined))
  } else {
    expandedProfile.value = p.id
  }
}

function saveProfiles(syncActiveId?: string): Promise<void> {
  return enqueue(() => doSaveProfiles(syncActiveId))
}

/** 对单个档案拉模型列表 / 测试连通（testLLM 接受任意组合） */
async function pullProfileModels(p: ProfileEdit) {
  profileBusy.value = p.id + ':pull'
  profileTest.value[p.id] = { ok: true, text: '拉取中…' }
  try {
    const r = await api.testLLM({ baseURL: p.baseURL, apiKey: p.apiKey || undefined, model: p.model })
    profileModels.value[p.id] = r.models || []
    profileTest.value[p.id] = r.error
      ? { ok: false, text: r.error }
      : { ok: true, text: `可用模型 ${r.models?.length ?? 0} 个，已填入下拉` }
  } finally {
    profileBusy.value = ''
  }
}
async function testProfile(p: ProfileEdit) {
  profileBusy.value = p.id + ':test'
  try {
    const r = await api.testLLM({ baseURL: p.baseURL, apiKey: p.apiKey || undefined, model: p.model })
    profileTest.value[p.id] = r.ok ? { ok: true, text: '连接成功 ✓' } : { ok: false, text: r.error || '连接失败' }
  } finally {
    profileBusy.value = ''
  }
}
const PRESETS: Record<string, { name: string; baseURL: string }> = {
  deepseek: { name: 'DeepSeek 官方', baseURL: 'https://api.deepseek.com' },
  gateway: { name: '本地 opencode 网关', baseURL: 'http://localhost:3000/api/v1' },
  zen: { name: 'OpenCode Zen', baseURL: 'https://opencode.ai/zen/v1' },
}
function applyPresetTo(p: ProfileEdit, key: string) {
  const pre = PRESETS[key]
  if (!pre) return
  p.name = p.name && p.name !== p.model ? p.name : pre.name
  p.mode = 'openai'
  p.baseURL = pre.baseURL
}
const keyInfo = reactive({ set: false, tail: '' })

const tabMeta = computed(() => TABS.find((t) => t.id === tab.value))

// ---------- 关于（版本信息） ----------
const ver = ref<VersionInfo | null>(null)
async function loadVersion() {
  try {
    ver.value = await api.getVersion()
  } catch {
    ver.value = null
  }
}

onMounted(async () => {
  await Promise.all([loadSettings(), refreshAgents(), refreshSkills(), refreshMcpStatus(), refreshTrash(), loadMemory(), refreshAutomations(), loadVersion()])
  loading.value = false
})

async function loadMemory() {
  try {
    form.memoryText = (await api.getMemory()).content
    form.memoryDirty = false
  } catch {
    /* 记忆加载失败不阻塞设置页 */
  }
}

async function refreshAutomations() {
  try {
    automations.value = (await api.listAutomations()).items
  } catch {
    automations.value = []
  }
}
async function createAutomation() {
  if (!newAuto.title.trim() || !newAuto.cron.trim() || !newAuto.prompt.trim()) return
  autoBusy.value = 'create'
  try {
    await api.createAutomation({ title: newAuto.title.trim(), cron: newAuto.cron.trim(), prompt: newAuto.prompt.trim() })
    newAuto.title = newAuto.cron = newAuto.prompt = ''
    await refreshAutomations()
    showToast('定时任务已创建 ✓')
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    autoBusy.value = ''
  }
}
// ---------- 权限规则 ----------
function addPermission() {
  form.permissions.push({ tool: '', pattern: '', effect: 'ask' })
}
function removePermission(i: number) {
  form.permissions.splice(i, 1)
}

// ---------- 斜杠命令 ----------
function addCommand() {
  form.commands.push({ name: '', template: '' })
}
function removeCommand(i: number) {
  form.commands.splice(i, 1)
}

async function toggleAutomation(a: Automation) {
  autoBusy.value = a.id
  try {
    await api.updateAutomation(a.id, { enabled: !a.enabled })
    await refreshAutomations()
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    autoBusy.value = ''
  }
}
async function deleteAutomation(a: Automation) {
  const ok = await askConfirm({
    title: `删除定时任务「${a.title}」`,
    message: '删除后不再执行，且立即释放名额。',
    confirmText: '删除',
  })
  if (!ok) return
  autoBusy.value = a.id
  try {
    await api.deleteAutomation(a.id)
    await refreshAutomations()
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    autoBusy.value = ''
  }
}
const fmtTime = (t?: string) => (t ? new Date(t).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '-')

async function saveMemory() {
  saving.value = true
  saveErr.value = ''
  try {
    await api.saveMemory(form.memoryText)
    form.memoryDirty = false
    showToast('长期记忆已保存 ✓')
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function loadSettings() {
  try {
    const s: SettingsView = await api.getSettings()
    form.mode = s.llm.mode
    form.baseURL = s.llm.baseURL
    form.model = s.llm.model
    form.temperature = s.llm.temperature
    form.maxHistory = s.llm.maxHistory
    keyInfo.set = s.llm.apiKeySet
    keyInfo.tail = s.llm.apiKeyTail
    form.readDirs = [...s.readDirs]
    form.projectsRoot = s.projectsRoot
    form.maxToolSteps = s.maxToolSteps
    form.maxTotalSteps = s.maxTotalSteps
    form.allowlistText = s.commandAllowlist.join(', ')
    form.maxTokens = s.llm.maxTokens || 0
    form.contextWindow = s.llm.contextWindow || 0
    form.smallModel = s.llm.smallModel || ''
    form.autoCompact = s.autoCompact !== false
    form.commandTimeoutSec = s.commandTimeoutSec || 0
    form.permissions = (s.permissions || []).map((r) => ({ ...r }))
    form.commands = (s.commands || []).map((c) => ({ ...c }))
    // 旧版本后端不下发 profiles 字段（新后端至少返回 []）：用于提示「请重启小柯」
    legacyBackend.value = !('profiles' in s)
    form.profiles = (s.profiles || []).map((p) => ({ ...p, apiKey: '', apiKeyTail: p.apiKeyTail || '' }))
    activeProfile.value = s.activeProfile || ''
    const ap = (s.profiles || []).find((p) => p.id === s.activeProfile)
    customLLM.value = !!ap && (ap.model !== s.llm.model || ap.baseURL !== s.llm.baseURL || ap.mode !== s.llm.mode)
    form.mcpServers = s.mcpServers.map((m) => ({
      ...m,
      args: [...m.args],
      // 参数按行编辑：保留路径中的空格，不再用空格分隔（Windows 路径常见坑）
      argLines: m.args.join('\n'),
      // 后端 omitempty：只有显式 true 会落盘；false/缺省都表示不启用只读过滤
      readOnly: m.readOnly === true,
    }))
  } catch (e) {
    saveErr.value = (e as Error).message
  }
}

async function refreshAgents() {
  try {
    agents.value = (await api.listAgents()).agents
  } catch {
    /* 忽略 */
  }
}
async function refreshSkills() {
  try {
    skills.value = (await api.listSkills()).skills
  } catch {
    /* 忽略 */
  }
}
async function refreshMcpStatus() {
  try {
    // 「刷新状态」同时收敛进程：退出/启动失败的 server 会重新拉起，正在运行的保持不动
    mcpStatuses.value = (await api.retryMCP()).servers
  } catch {
    mcpStatuses.value = []
  }
}

const statusOf = (name: string): MCPStatus | undefined => mcpStatuses.value.find((s) => s.name === name)

// ---------- 回收站 ----------
// 多选：选中的回收站目录名集合（声明在 refreshTrash 之前，避免依赖声明顺序）
const trashSelected = ref<string[]>([])

async function refreshTrash() {
  try {
    trashItems.value = (await api.listTrash()).items
  } catch {
    trashItems.value = []
  }
  // 刷新后清空选中（列表可能变了，旧选中没有意义）
  trashSelected.value = []
}

const trashAllSelected = computed(
  () => trashItems.value.length > 0 && trashSelected.value.length === trashItems.value.length,
)
const trashSomeSelected = computed(
  () => trashSelected.value.length > 0 && trashSelected.value.length < trashItems.value.length,
)

function isTrashSelected(dir: string): boolean {
  return trashSelected.value.includes(dir)
}

function toggleTrashSelect(dir: string) {
  const i = trashSelected.value.indexOf(dir)
  if (i >= 0) trashSelected.value.splice(i, 1)
  else trashSelected.value.push(dir)
}

function toggleTrashSelectAll() {
  trashSelected.value = trashAllSelected.value ? [] : trashItems.value.map((it) => it.dir)
}

// 全选复选框的 indeterminate 态（部分选中）
const trashAllRef = ref<HTMLInputElement | null>(null)
watchEffect(() => {
  if (trashAllRef.value) trashAllRef.value.indeterminate = trashSomeSelected.value
})

async function purgeTrashSelected() {
  if (trashBusy.value || !trashSelected.value.length) return
  const n = trashSelected.value.length
  const ok = await askConfirm({
    title: `彻底删除选中的 ${n} 条记录`,
    message: `这 ${n} 条会话将从回收站永久移除，无法再恢复。`,
    confirmText: `彻底删除 ${n} 条`,
  })
  if (!ok) return
  trashBusy.value = 'batch'
  try {
    const r = await api.purgeTrashBatch([...trashSelected.value])
    await refreshTrash()
    if (r.failed) {
      showToast(`已删除 ${r.ok} 条，${r.failed} 条失败`)
    } else {
      showToast(`已彻底删除 ${r.ok} 条`)
    }
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    trashBusy.value = ''
  }
}

async function restoreTrashSelected() {
  if (trashBusy.value || !trashSelected.value.length) return
  const n = trashSelected.value.length
  trashBusy.value = 'batch'
  try {
    const r = await api.restoreTrashBatch([...trashSelected.value])
    await refreshTrash()
    if (r.failed) {
      showToast(`已恢复 ${r.ok} 条，${r.failed} 条跳过（可能会话仍存在）`)
    } else {
      showToast(`已恢复 ${r.ok} 条，可在侧栏会话列表看到`)
    }
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    trashBusy.value = ''
  }
}

async function restoreTrashItem(it: TrashItem) {
  if (trashBusy.value) return
  trashBusy.value = it.dir
  try {
    await api.restoreTrash(it.dir)
    await refreshTrash()
    showToast(`已恢复「${it.title || it.id}」，可在侧栏会话列表看到`)
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    trashBusy.value = ''
  }
}

async function purgeTrashItem(it: TrashItem) {
  if (trashBusy.value) return
  const ok = await askConfirm({
    title: `彻底删除「${it.title || it.id}」`,
    message: '这条会话将从回收站永久移除，无法再恢复。',
    confirmText: '彻底删除',
  })
  if (!ok) return
  trashBusy.value = it.dir
  try {
    await api.purgeTrash(it.dir)
    await refreshTrash()
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    trashBusy.value = ''
  }
}

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function fmtDeletedAt(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function showToast(msg: string) {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2200)
}

// ---------- 模型区 ----------
/** 摘要条的来源标注：激活档案名，或「未绑定档案」 */
const activeProfileName = computed(() => {
  const ap = form.profiles.find((p) => p.id === activeProfile.value)
  return ap ? ap.name || '（未命名）' : ''
})

// ---------- MCP 区 ----------
function addFilesystemServer() {
  const dir = newMcp.dir.trim()
  if (!dir) return
  const n = form.mcpServers.filter((m) => m.name.startsWith('fs-')).length + 1
  const args = ['-y', '@modelcontextprotocol/server-filesystem', dir]
  form.mcpServers.push({
    name: `fs-${n}`,
    command: 'npx',
    args,
    argLines: args.join('\n'),
    enabled: true,
    // 官方 filesystem server 是可读写的：默认按只读过滤，避免承诺「只读」却给写权限
    readOnly: true,
  })
  newMcp.dir = ''
}

function addManualServer() {
  if (!newMcp.name.trim() || !newMcp.command.trim()) return
  const args = newMcp.argsText
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean)
  form.mcpServers.push({
    name: newMcp.name.trim(),
    command: newMcp.command.trim(),
    args,
    argLines: args.join('\n'),
    enabled: true,
    readOnly: false,
  })
  newMcp.name = ''
  newMcp.command = ''
  newMcp.argsText = ''
}

// ---------- 目录区 ----------
function addReadDir() {
  const d = form.newReadDir.trim()
  if (!d || form.readDirs.includes(d)) return
  form.readDirs.push(d)
  form.newReadDir = ''
}

// ---------- 保存 ----------
function buildPayload() {
  return {
        llm: {
          mode: form.mode,
          baseURL: form.baseURL.trim(),
          apiKey: form.apiKey,
          model: form.model.trim(),
          temperature: Number(form.temperature) || 0.7,
          maxHistory: Number(form.maxHistory) || 20,
          maxTokens: Math.max(0, Number(form.maxTokens) || 0),
          contextWindow: Math.max(0, Number(form.contextWindow) || 0),
          smallModel: form.smallModel.trim(),
        },
        mcpServers: form.mcpServers.map((m) => ({
          name: m.name.trim(),
          command: m.command.trim(),
          args: (m.argLines || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
          env: m.env,
          enabled: m.enabled,
          readOnly: !!m.readOnly,
        })),
        readDirs: form.readDirs.map((d) => d.trim()).filter(Boolean),
        projectsRoot: form.projectsRoot.trim(),
        maxToolSteps: Number(form.maxToolSteps) || 20,
        maxTotalSteps: Number(form.maxTotalSteps) || 0,
        commandAllowlist: form.allowlistText.split(/[,,\n]/).map((s) => s.trim()).filter(Boolean),
        autoCompact: form.autoCompact !== false,
        compactKeepCount: 0,
        commandTimeoutSec: Math.max(0, Number(form.commandTimeoutSec) || 0),
        permissions: form.permissions.filter((r) => r.tool.trim()).map((r) => ({
          tool: r.tool.trim(),
          pattern: (r.pattern || '').trim(),
          effect: r.effect,
        })),
        commands: form.commands.filter((c) => c.name.trim() && c.template.trim()).map((c) => ({
          name: c.name.trim().replace(/^\/+/, ''),
          template: c.template.trim(),
        })),
        llmProfiles: form.profiles
          .filter((p) => p.name.trim() || p.model.trim() || p.baseURL.trim())
          .map((p) => ({
            id: p.id, name: p.name.trim(), mode: p.mode, baseURL: p.baseURL.trim(),
            apiKey: p.apiKey, model: p.model.trim(), smallModel: p.smallModel?.trim() || '',
          })),
        // 激活指向必须随请求体回传：后端 Update 是整字段替换，漏了就等于解绑
        activeProfile: activeProfile.value,
      }
}

async function save() {
  saving.value = true
  saveErr.value = ''
  try {
    await api.saveSettings(
      buildPayload(),
    )
    showToast('已保存并应用 ✓')
    // 不自动关闭：避免在智能体/技能 tab 有未保存编辑时被一起关掉
  } catch (e) {
    saveErr.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="settings-page">
    <!-- 头部 -->
    <header class="sp-head">
      <button class="back" title="返回对话" @click="emit('close')">
        <Icon name="arrow-left" :size="15" />
        返回对话
      </button>
      <div class="sp-title">
        <span class="sp-icon"><Icon name="settings" :size="18" /></span>
        <div>
          <div class="sp-name">设置中心</div>
          <div class="sp-desc">{{ tabMeta?.desc }}</div>
        </div>
      </div>
      <button class="save-btn" :disabled="saving" @click="save">
        <Icon name="save" :size="15" v-if="!saving" />
        {{ saving ? '保存中…' : '保存并应用' }}
      </button>
    </header>

    <div v-if="loading" class="loading">加载中…</div>

    <div v-else class="sp-body">
      <!-- 左：导航 -->
      <nav class="sp-nav">
        <button
          v-for="t in TABS"
          :key="t.id"
          class="nav-item"
          :class="{ active: tab === t.id }"
          @click="tab = t.id"
        >
          <span class="nav-icon"><Icon :name="t.icon" :size="17" /></span>
          <span class="nav-text">
            <span class="nav-label">{{ t.label }}</span>
            <span class="nav-desc">{{ t.desc }}</span>
          </span>
        </button>
      </nav>

      <!-- 右：内容 -->
      <main class="sp-content">
        <!-- 智能体 -->
        <AgentSection v-if="tab === 'agents'" :agents="agents" @changed="refreshAgents" />

        <!-- 技能 -->
        <SkillSection v-else-if="tab === 'skills'" :skills="skills" @changed="refreshSkills" />

        <!-- 模型 -->
        <section v-else-if="tab === 'llm'" class="panel">
          <div class="panel-head">
            <h2>模型 API</h2>
            <p>任意 OpenAI 兼容上游。模型配置以「档案」为单位管理：添加多套，随时一键切换，下一条消息生效。</p>
          </div>

          <!-- 旧版本后端：档案能力未加载，提示重启 -->
          <div v-if="legacyBackend" class="card legacy-banner">
            <Icon name="warning" :size="15" />
            <span>
              检测到服务端是<b>旧版本</b>：模型配置档案需要重启小柯后才会生效。
              重启时会把当前正在使用的配置自动保存为第一个档案；本页的保存/切换按钮暂不可用。
            </span>
          </div>

          <!-- 当前生效（只读摘要）：来源 = 激活档案，或手动自定义 -->
          <div class="card active-summary">
            <span class="pr-badge" :class="form.mode">{{ form.mode === 'mock' ? '演示' : '在线' }}</span>
            <span class="as-main">
              <b>{{ form.model || '未设置模型' }}</b>
              <i>{{ form.baseURL || '—' }}<template v-if="keyInfo.set"> · key 尾号 {{ keyInfo.tail }}</template></i>
            </span>
            <span v-if="activeProfileName" class="as-source">来自档案「{{ activeProfileName }}」</span>
            <span v-else class="as-source warn">未绑定档案（手动自定义）</span>
            <!-- 当前生效值没有档案载体或与激活档案漂移时：一键收编/同步（key 服务端直取） -->
            <template v-if="!legacyBackend && !activeProfileName && !customLLM">
              <!-- 未绑定档案：起个名字，把当前生效配置（含 Key）收编为第一张档案 -->
              <template v-if="adoptOpen">
                <input
                  v-model="adoptName"
                  class="inp adopt-name"
                  :placeholder="'给这套配置起个名字，如 ' + shortModelName"
                  @keydown.enter.prevent="confirmAdopt"
                />
                <button class="ab-btn primary" :disabled="adoptBusy" @click="confirmAdopt">{{ adoptBusy ? '保存中…' : '保存' }}</button>
                <button class="ab-btn ghost" @click="adoptOpen = false">取消</button>
              </template>
              <button v-else class="btn ghost tiny adopt-btn" @click="adoptOpen = true; adoptName = ''">
                ↗ 保存为档案
              </button>
            </template>
            <button
              v-else-if="!legacyBackend && customLLM"
              class="btn ghost tiny adopt-btn"
              :disabled="adoptBusy"
              title="当前生效值与激活档案不一致：把生效值（含 Key）写回激活档案"
              @click="adoptCurrent"
            >
              {{ adoptBusy ? '同步中…' : '⤴ 同步到档案' }}
            </button>
          </div>

          <!-- 模型配置档案 -->
          <div class="card profile-card">
            <div class="card-title">
              模型配置档案（{{ form.profiles.length }}/20）
              <span v-if="customLLM" class="custom-flag" title="当前生效值被手动改过，与激活档案不同">已手动自定义</span>
            </div>
            <div class="hint">
              每个档案是完整的一套配置（名称/模式/地址/Key/主模型/轻量模型），点「使用」即时切换（连运行模式一起切）。
              编辑收起即自动保存。
            </div>
            <div v-if="!form.profiles.length" class="empty-box">
              <template v-if="!legacyBackend && form.model">
                当前正在使用的「{{ form.model }}」还没有保存为档案——点下面按钮一键收编（Key 在服务端直取，不经页面）：
                <div class="eb-action">
                  <button class="btn primary tiny" :disabled="adoptBusy" @click="confirmAdopt">
                    {{ adoptBusy ? '保存中…' : '把当前配置保存为第一个档案' }}
                  </button>
                </div>
              </template>
              <template v-else-if="!legacyBackend">还没有配置档案，点「添加配置」新建一套。</template>
            </div>
            <div v-for="(p, pi) in form.profiles" :key="p.id" class="profile-item" :class="{ active: p.id === activeProfile, open: expandedProfile === p.id }">
              <!-- 摘要行 -->
              <div class="profile-row" @click="toggleProfileEdit(p)">
                <span class="pr-badge" :class="p.mode">{{ p.mode === 'mock' ? '演示' : '在线' }}</span>
                <span class="pr-main">
                  <b>{{ p.name || '（未命名）' }}</b>
                  <i>{{ p.model || '未设模型' }}<template v-if="p.baseURL"> · {{ p.baseURL }}</template><template v-if="p.apiKeySet"> · key 尾号 {{ p.apiKeyTail }}</template></i>
                </span>
                <button class="ab-btn primary" :class="{ used: p.id === activeProfile }" :disabled="profileBusy === p.id" @click.stop="p.id === activeProfile ? undefined : activateNow(p.id)">
                  {{ p.id === activeProfile ? '✓ 使用中' : (profileBusy === p.id ? '切换中…' : '使用') }}
                </button>
                <button class="ab-btn danger" title="删除档案" @click.stop="removeProfile(pi)">
                  <Icon name="trash" :size="13" />
                </button>
                <span class="pr-chevron" title="展开/收起" @click.stop="toggleProfileEdit(p)">
                  <Icon :name="expandedProfile === p.id ? 'chevron-up' : 'chevron-down'" :size="15" />
                </span>
              </div>
              <!-- 展开编辑 -->
              <div v-if="expandedProfile === p.id" class="profile-edit" @click.stop>
                <div class="pe-row">
                  <input v-model="p.name" class="inp pe-name" placeholder="配置名，如 DeepSeek 官方" />
                  <select v-model="p.mode" class="inp pe-mode">
                    <option value="openai">OpenAI 兼容上游</option>
                    <option value="mock">mock 演示（免 Key）</option>
                  </select>
                </div>
                <div v-if="p.mode === 'openai'" class="pe-presets">
                  <button v-for="(pre, key) in { deepseek: 'DeepSeek 官方', gateway: '本地网关', zen: 'OpenCode Zen' }" :key="key" class="chip" @click="applyPresetTo(p, key)">{{ pre }}</button>
                </div>
                <template v-if="p.mode === 'openai'">
                  <input v-model="p.baseURL" class="inp" placeholder="Base URL，如 https://api.deepseek.com" />
                  <input v-model="p.apiKey" class="inp" type="password" autocomplete="off"
                    :placeholder="p.apiKeySet ? `已配置（尾号 ${p.apiKeyTail}），留空保持不变` : 'sk-… / oc-…'" />
                  <div class="model-line">
                    <input v-model="p.model" class="inp" :list="'pm-' + p.id" placeholder="主模型名" />
                    <datalist :id="'pm-' + p.id">
                      <option v-for="m in profileModels[p.id] || []" :key="m" :value="m" />
                    </datalist>
                    <button class="btn ghost" :disabled="profileBusy === p.id + ':pull'" @click="pullProfileModels(p)">
                      {{ profileBusy === p.id + ':pull' ? '拉取中…' : '拉取列表' }}
                    </button>
                    <button class="btn ghost" :disabled="profileBusy === p.id + ':test'" @click="testProfile(p)">
                      {{ profileBusy === p.id + ':test' ? '测试中…' : '测试连接' }}
                    </button>
                  </div>
                  <input v-model="p.smallModel" class="inp" :list="'pm-' + p.id" placeholder="轻量模型（可选，留空 = 主模型）" />
                  <div v-if="profileTest[p.id]" class="test" :class="profileTest[p.id].ok ? 'ok' : 'bad'">{{ profileTest[p.id].text }}</div>
                </template>
                <div class="pe-foot">
                  <span class="hint">收起本档案（再点一次摘要行）也会自动保存。</span>
                  <button class="btn primary tiny" :disabled="profileSaving === p.id" @click="saveProfile(p)">
                    {{ profileSaving === p.id ? '保存中…' : '保存' }}
                  </button>
                </div>
              </div>
            </div>
            <button class="btn ghost tiny" :disabled="form.profiles.length >= 20" @click="addProfile">
              <Icon name="plus" :size="13" /> 添加配置
            </button>
          </div>

          <div class="grid">
            <div class="card">
              <div class="card-title">温度</div>
              <input v-model="form.temperature" class="inp" type="number" step="0.1" min="0" max="2" />
              <div class="hint">所有档案共享的全局生成参数。</div>
            </div>
            <div class="card">
              <div class="card-title">历史条数上限</div>
              <input v-model="form.maxHistory" class="inp" type="number" min="4" max="100" />
            </div>
          </div>
          <div class="note"><Icon name="sparkles" :size="14" /> 思考程度在对话页右下角随时调节，无需进设置。轻量模型、上下文窗口、命令超时等在「高级」页。</div>
        </section>

        <!-- MCP -->
        <section v-else-if="tab === 'mcp'" class="panel">
          <div class="panel-head">
            <h2>MCP 服务</h2>
            <p>每个服务是一个本机常驻进程（stdio）。参数每行一个，路径里的空格会原样保留。</p>
            <button class="btn ghost tiny" @click="refreshMcpStatus">
              <Icon name="refresh" :size="13" />
              刷新并重试
            </button>
          </div>

          <div v-for="(m, i) in form.mcpServers" :key="i" class="mcp-card" :class="{ off: !m.enabled }">
            <div class="mcp-top">
              <label class="chk"><input type="checkbox" v-model="m.enabled" /> 启用</label>
              <label class="chk" title="只读：不把 write/edit/create/move/delete 等工具暴露给模型">
                <input type="checkbox" v-model="m.readOnly" /> 只读
              </label>
              <b class="mcp-name">{{ m.name }}</b>
              <button class="btn tiny danger-ghost" @click="form.mcpServers.splice(i, 1)">删除</button>
            </div>
            <code class="mcp-cmd">{{ m.command }} {{ m.argLines.replace(/\n/g, ' ') }}</code>
            <textarea v-model="m.argLines" class="inp wide mcp-args" rows="2" placeholder="参数，每行一个"></textarea>
            <div v-if="statusOf(m.name)" class="mcp-status" :class="statusOf(m.name)!.running ? 'ok' : 'bad'">
              <span class="mcp-dot"></span>
              <span>{{ statusOf(m.name)!.running ? `运行中 · ${statusOf(m.name)!.tools.length} 个工具` : (statusOf(m.name)!.error || (statusOf(m.name)!.starting ? '启动中…' : '未运行')) }}</span>
              <span v-if="statusOf(m.name)!.toolHint" class="mcp-hint">（{{ statusOf(m.name)!.toolHint }}）</span>
              <span v-if="m.readOnly" class="mcp-ro">只读过滤</span>
            </div>
          </div>
          <div v-if="!form.mcpServers.length" class="empty-box">还没有 MCP 服务，用下面的方式添加。</div>

          <div class="card">
            <div class="card-title">一键添加 filesystem 服务（默认只读）</div>
            <div class="add-row">
              <input v-model="newMcp.dir" class="inp" placeholder="授权查看的目录绝对路径，如 D:\code\my-project" />
              <button class="btn primary" @click="addFilesystemServer">
                <Icon name="plus" :size="13" :stroke="2.2" />
                添加
              </button>
            </div>
            <div class="hint">官方 filesystem server 本身可读写；默认勾选「只读」后，write/edit/create/move/delete 类工具不会暴露给模型。</div>
          </div>
          <div class="card">
            <div class="card-title">手动添加其它 MCP 服务</div>
            <div class="manual-grid">
              <input v-model="newMcp.name" class="inp" placeholder="名称，如 git-tools" />
              <input v-model="newMcp.command" class="inp" placeholder="命令，如 npx / python / node" />
              <textarea v-model="newMcp.argsText" class="inp wide" rows="3" placeholder="参数，每行一个（路径可含空格）"></textarea>
              <button class="btn ghost" @click="addManualServer">
                <Icon name="plus" :size="13" :stroke="2.2" />
                添加
              </button>
            </div>
          </div>
        </section>

        <!-- 目录 -->
        <section v-else-if="tab === 'dirs'" class="panel">
          <div class="panel-head">
            <h2>目录与项目</h2>
            <p>只读目录：小柯可以查看这些目录里的代码（不可修改）。</p>
          </div>
          <div class="card">
            <div class="card-title">只读授权目录</div>
            <div v-for="(d, i) in form.readDirs" :key="d" class="dir-row">
              <code>{{ d }}</code>
              <button class="btn tiny danger-ghost" @click="form.readDirs.splice(i, 1)">移除</button>
            </div>
            <div v-if="!form.readDirs.length" class="empty-box">尚未授权任何目录。</div>
            <div class="add-row" style="margin-top: 10px">
              <input v-model="form.newReadDir" class="inp" placeholder="输入绝对路径" @keydown.enter="addReadDir" />
              <button class="btn primary" @click="addReadDir">添加目录</button>
            </div>
          </div>
          <div class="card">
            <div class="card-title">项目根目录</div>
            <input v-model="form.projectsRoot" class="inp" :placeholder="`留空 = 默认（${form.projectsRoot || '小柯目录'}\\projects）`" />
          </div>
        </section>

        <!-- 高级 -->
        <section v-else-if="tab === 'adv'" class="panel">
          <div class="panel-head">
            <h2>高级</h2>
            <p>工具循环、上下文与命令执行的边界控制。</p>
          </div>
          <div class="grid">
            <div class="card">
              <div class="card-title">单段工具轮数</div>
              <input v-model="form.maxToolSteps" class="inp" type="number" min="1" max="200" />
              <div class="hint">一段连续工具调用的轮数。到达后不中断任务，而是自动续一段继续做。</div>
            </div>
            <div class="card">
              <div class="card-title">总轮数预算</div>
              <input v-model="form.maxTotalSteps" class="inp" type="number" min="0" max="1000" />
              <div class="hint">
                单次回复允许的工具轮数总和（含所有续跑段），长任务的真正天花板。
                留空或 0 = 用默认 120；把它调成和单段一样 = 不续跑。
                建议 120~300；调到 500 以上单次回复会跑很久、花费也高。
              </div>
            </div>
            <div class="card">
              <div class="card-title">命令白名单（逗号分隔）</div>
              <input v-model="form.allowlistText" class="inp" placeholder="git, npm, node, python, pip" />
              <div class="hint">run_command 只允许执行这里的命令，argv 直传不经 shell。</div>
            </div>
            <div class="card">
              <div class="card-title">单次回复长度上限（token，可选）</div>
              <input v-model="form.maxTokens" class="inp" type="number" min="0" placeholder="留空或 0 = 上游默认" />
              <div class="hint">注入请求体 max_tokens。设太小会把回答拦腰截断，不确定就留空。</div>
            </div>
            <div class="card">
              <div class="card-title">上下文窗口（token，可选）</div>
              <input v-model="form.contextWindow" class="inp" type="number" min="0" placeholder="留空或 0 = 未知（按字节水位触发压缩）" />
              <div class="hint">填模型的真实上下文窗口（如 DeepSeek 128000），自动压缩会按 token 水位判断「什么时候该压缩」，比按字节数估算更准。</div>
            </div>
            <div class="card">
              <div class="card-title">命令超时（秒，0 = 默认 30）</div>
              <input v-model="form.commandTimeoutSec" class="inp" type="number" min="0" max="600" />
              <div class="hint">npm install / 构建类命令可能超 30 秒，超时就调大（上限 600）。</div>
            </div>
            <div class="card">
              <div class="card-title">
                <label class="check-row">
                  <input v-model="form.autoCompact" type="checkbox" />
                  上下文自动压缩
                </label>
              </div>
              <div class="hint">
                历史超过预算时，自动把较早的回合总结成摘要（用当前模型，约几秒），
                保留更早的语境；关闭后超长历史会被直接裁掉（更快，但丢语境）。
              </div>
            </div>
            <div class="card">
              <div class="card-title">长期记忆（全局）</div>
              <div class="hint">
                跨会话保留的知识：用户偏好、项目结构、纠正过的错误。小柯会通过 memory_append
                工具自己沉淀，也可以在这里手工整理；每个会话开始时自动注入。项目级记忆由小柯按项目维护，不在这里展示。
              </div>
              <textarea
                v-model="form.memoryText"
                class="inp memory-editor"
                rows="8"
                placeholder="- 用户偏好简洁的中文回复&#10;- 项目 X 的构建命令是 npm run build"
                @input="form.memoryDirty = true"
              ></textarea>
              <div class="memory-actions">
                <button class="btn primary" :disabled="saving || !form.memoryDirty" @click="saveMemory">
                  {{ saving ? '保存中…' : '保存记忆' }}
                </button>
                <span v-if="form.memoryText" class="hint-inline">{{ form.memoryText.length }} 字符</span>
              </div>
            </div>

            <div class="card">
              <div class="card-title">权限规则（{{ form.permissions.length }}/64）</div>
              <div class="hint">
                按书写顺序评估、取最后命中的规则（last-match-wins）。工具名支持 * ? 通配；
                资源模式匹配参数摘要（如 path=hello.txt、command=git status），留空 = 任意参数。
                effect：allow 直接放行 / ask 弹窗审批 / deny 直接拒绝。
              </div>
              <div v-for="(r, ri) in form.permissions" :key="ri" class="rule-row">
                <input v-model="r.tool" class="inp rule-tool" placeholder="工具名，如 write_file 或 run_command" />
                <input v-model="r.pattern" class="inp rule-pattern" placeholder="资源模式（可空），如 git status* 或 *.env" />
                <select v-model="r.effect" class="inp rule-effect">
                  <option value="allow">allow</option>
                  <option value="ask">ask</option>
                  <option value="deny">deny</option>
                </select>
                <button class="ab-btn danger" @click="removePermission(ri)">
                  <Icon name="trash" :size="13" />
                </button>
              </div>
              <button class="btn ghost tiny" @click="addPermission">
                <Icon name="plus" :size="13" /> 添加规则
              </button>
            </div>

            <div class="card">
              <div class="card-title">斜杠命令（{{ form.commands.length }}/64）</div>
              <div class="hint">
                输入框里敲 / 触发补全。模板变量：$ARGUMENTS = 全部参数文本；$1..$9 = 按空白分词的位置参数。
                例：模板「把 $ARGUMENTS 翻译成英文」→ 输入 /tr 你好世界 → 发送「把 你好世界 翻译成英文」。
              </div>
              <div v-for="(c, ci) in form.commands" :key="ci" class="cmd-row">
                <input v-model="c.name" class="inp cmd-name" placeholder="命令名（不带 /）" />
                <textarea v-model="c.template" class="inp cmd-template" rows="2" placeholder="提示词模板，如：把 $ARGUMENTS 整理成待办清单"></textarea>
                <button class="ab-btn danger" @click="removeCommand(ci)">
                  <Icon name="trash" :size="13" />
                </button>
              </div>
              <button class="btn ghost tiny" @click="addCommand">
                <Icon name="plus" :size="13" /> 添加命令
              </button>
            </div>

            <div class="card">
              <div class="card-title">定时任务（{{ automations.length }}/20）</div>
              <div class="hint">
                按 cron 表达式（本地时区，5 段：分 时 日 月 周）无人值守执行一段指令，
                每次运行新建独立会话；小柯也可以在对话里自己创建。构建/部署提醒、定时汇总都是典型用法。
              </div>
              <div v-if="automations.length" class="auto-list">
                <div v-for="a in automations" :key="a.id" class="auto-row" :class="{ off: !a.enabled }">
                  <div class="auto-main">
                    <b>{{ a.title }}</b>
                    <code>{{ a.cron }}</code>
                    <span class="hint-inline">
                      已运行 {{ a.runCount }} 次 · 下次 {{ fmtTime(a.nextRunAt) }}
                      <template v-if="a.lastError"> · ⚠ {{ a.lastError.slice(0, 40) }}</template>
                    </span>
                  </div>
                  <div class="auto-ops">
                    <button class="ab-btn ghost" :disabled="autoBusy === a.id" @click="toggleAutomation(a)">
                      {{ a.enabled ? '停用' : '启用' }}
                    </button>
                    <button class="ab-btn danger" :disabled="autoBusy === a.id" @click="deleteAutomation(a)">删除</button>
                  </div>
                </div>
              </div>
              <p v-else class="hint-inline">还没有定时任务。</p>
              <div class="auto-new">
                <input v-model="newAuto.title" class="inp" placeholder="任务名（如「每天早上9点汇总待办」）" />
                <input v-model="newAuto.cron" class="inp" placeholder="cron 表达式，如 0 9 * * * 或 */30 * * * *" />
                <textarea v-model="newAuto.prompt" class="inp" rows="2" placeholder="每次触发执行的指令（自包含）"></textarea>
                <button class="btn primary" :disabled="autoBusy === 'create' || !newAuto.title || !newAuto.cron || !newAuto.prompt" @click="createAutomation">
                  {{ autoBusy === 'create' ? '创建中…' : '创建任务' }}
                </button>
              </div>
            </div>
          </div>

          <div class="card backup-card">
            <div class="card-title">数据备份</div>
            <div class="hint">
              删除会话时会先保留一份到 <code>data/sessions/trash/</code>，可在下方「回收站」里恢复。
              服务启动时和之后每 24 小时会把全部会话打包到 <code>data/backups/</code>（保留最近 14 份）。
              随时可以手动下载一份完整备份。
            </div>
            <div class="backup-actions">
              <a class="btn ghost" href="/api/backup/sessions" download>
                <Icon name="download" :size="14" /> 下载全部会话（ZIP）
              </a>
              <span class="hint">备份含会话快照与未合并日志，可直接解压查看。</span>
            </div>
          </div>

          <div class="card trash-card">
            <div class="card-title trash-head">
              <span>回收站</span>
              <button class="btn ghost tiny" :disabled="!!trashBusy" @click="refreshTrash">
                <Icon name="refresh" :size="12" /> 刷新
              </button>
            </div>
            <div class="hint">
              删除的会话会在这里保留 30 天，随时可以恢复。彻底删除不可撤销。
            </div>
            <div v-if="!trashItems.length" class="trash-empty">
              回收站是空的。删除会话后会在这里留一份副本。
            </div>
            <template v-else>
              <!-- 批量操作栏：全选 + 选中计数 + 批量按钮 -->
              <div class="trash-batch">
                <label class="tb-all">
                  <input
                    ref="trashAllRef"
                    type="checkbox"
                    :checked="trashAllSelected"
                    :disabled="!!trashBusy"
                    @change="toggleTrashSelectAll"
                  />
                  <span>全选</span>
                </label>
                <span class="tb-count">
                  {{ trashSelected.length ? `已选 ${trashSelected.length} / ${trashItems.length}` : `共 ${trashItems.length} 条` }}
                </span>
                <div class="tb-actions">
                  <button
                    class="btn ghost tiny"
                    :disabled="!!trashBusy || !trashSelected.length"
                    title="批量恢复到会话列表"
                    @click="restoreTrashSelected"
                  >
                    <Icon name="refresh" :size="12" />
                    恢复选中
                  </button>
                  <button
                    class="btn danger tiny"
                    :disabled="!!trashBusy || !trashSelected.length"
                    title="批量彻底删除，不可恢复"
                    @click="purgeTrashSelected"
                  >
                    <Icon name="trash" :size="12" />
                    删除选中
                  </button>
                </div>
              </div>

              <div class="trash-list">
                <div
                  v-for="it in trashItems"
                  :key="it.dir"
                  class="trash-item"
                  :class="{ selected: isTrashSelected(it.dir) }"
                >
                  <label class="ti-check" @click.stop>
                    <input
                      type="checkbox"
                      :checked="isTrashSelected(it.dir)"
                      :disabled="!!trashBusy"
                      @change="toggleTrashSelect(it.dir)"
                    />
                  </label>
                  <div class="ti-main">
                    <div class="ti-title">{{ it.title || '（无标题会话）' }}</div>
                    <div class="ti-meta">
                      {{ fmtDeletedAt(it.deletedAt) }} 删除 · {{ it.messageCount }} 条消息 · {{ fmtSize(it.sizeBytes) }}
                    </div>
                  </div>
                  <div class="ti-actions">
                    <button
                      class="btn ghost tiny"
                      :disabled="!!trashBusy"
                      title="恢复到会话列表"
                      @click="restoreTrashItem(it)"
                    >
                      <Icon name="refresh" :size="12" />
                      {{ trashBusy === it.dir ? '恢复中…' : '恢复' }}
                    </button>
                    <button
                      class="btn danger tiny"
                      :disabled="!!trashBusy"
                      title="彻底删除，不可恢复"
                      @click="purgeTrashItem(it)"
                    >
                      <Icon name="trash" :size="12" /> 彻底删除
                    </button>
                  </div>
                </div>
              </div>
            </template>
          </div>
        </section>

        <section v-else class="panel">
          <div class="panel-head">
            <h2>关于</h2>
            <p>当前版本与本次更新内容。更新说明由发版脚本从 git 提交记录生成，只保留最新一版。</p>
          </div>

          <div class="grid">
            <div class="card about-card">
              <div class="card-title">当前版本</div>
              <div class="about-ver">
                <span class="about-num">v{{ ver?.version || '—' }}</span>
                <span v-if="ver?.date" class="about-date">发布于 {{ ver.date }}</span>
              </div>
              <div class="about-app">
                <b>小柯</b>
                <span>专属智能体 · Go + Vue · 本地运行</span>
              </div>
            </div>

            <div class="card about-notes">
              <div class="card-title">本次更新</div>
              <div v-if="ver?.notes" class="about-md md" v-html="renderMarkdown(ver.notes)"></div>
              <div v-else class="hint">暂无更新说明（可能是后端版本较旧，或尚未发版）。</div>
            </div>
          </div>
        </section>

        <div v-if="saveErr" class="err-bar"><Icon name="warning" :size="14" /> {{ saveErr }}</div>
      </main>
    </div>

    <transition name="toast">
      <div v-if="toast" class="toast">{{ toast }}</div>
    </transition>
  </div>
</template>

<style scoped>
/* ---------- 关于 ---------- */
.about-card {
  justify-content: center;
  gap: 10px;
}

.about-ver {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.about-num {
  font-size: 28px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--accent);
  font-variant-numeric: tabular-nums;
}

.about-date {
  font-size: 11px;
  color: var(--text-faint);
}

.about-app {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 12px;
  color: var(--text-muted);
}

.about-app b {
  color: var(--text);
  font-size: 13px;
}

.about-notes {
  max-height: 420px;
  overflow-y: auto;
}

.about-md {
  margin-top: 10px;
  font-size: 12.5px;
  line-height: 1.75;
  color: var(--text-muted);
}

.about-md :deep(h1),
.about-md :deep(h2),
.about-md :deep(h3) {
  margin: 14px 0 8px;
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
}

.about-md :deep(h1:first-child),
.about-md :deep(h2:first-child),
.about-md :deep(h3:first-child) {
  margin-top: 0;
}

.about-md :deep(ul) {
  margin: 6px 0;
  padding-left: 18px;
}

.about-md :deep(li) {
  margin: 4px 0;
}

.about-md :deep(code) {
  background: rgba(255, 255, 255, 0.07);
  border-radius: 4px;
  padding: 1px 5px;
  font-size: 11.5px;
}

.settings-page {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: flex;
  flex-direction: column;
  background:
    radial-gradient(900px 520px at 8% -10%, rgba(255, 154, 61, 0.09), transparent 60%),
    radial-gradient(720px 460px at 96% 0%, rgba(167, 139, 250, 0.08), transparent 60%),
    var(--bg);
}

/* ---------- 头部 ---------- */
.sp-head {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 18px;
  padding: 15px 28px;
  background: rgba(10, 11, 15, 0.62);
  border-bottom: 1px solid var(--border);
  backdrop-filter: blur(20px);
  flex-shrink: 0;
}

.back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: 10px;
  padding: 8px 14px;
  font-size: 12.5px;
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.back:hover {
  color: var(--text);
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.07);
}

.sp-title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  min-width: 0;
}

.sp-icon {
  width: 38px;
  height: 38px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(145deg, rgba(255, 154, 61, 0.16), rgba(255, 154, 61, 0.05));
  border: 1px solid rgba(255, 154, 61, 0.2);
  color: var(--accent);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.sp-name {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: -0.01em;
}

.sp-desc {
  margin-top: 1px;
  font-size: 11.5px;
  color: var(--text-faint);
}

.save-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  background: var(--accent-gradient);
  color: var(--accent-text);
  border: none;
  border-radius: 10px;
  padding: 10px 20px;
  font-size: 12.5px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: var(--shadow-accent);
  transition: transform 0.15s var(--ease), filter 0.15s ease;
}

.save-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  filter: brightness(1.04);
}

.save-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.loading {
  padding: 60px;
  text-align: center;
  color: var(--text-faint);
}

/* ---------- 主体 ---------- */
.sp-body {
  flex: 1;
  display: flex;
  min-height: 0;
}

.sp-nav {
  width: 222px;
  flex-shrink: 0;
  padding: 20px 14px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  border-right: 1px solid var(--border);
  background: rgba(9, 10, 14, 0.4);
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 10px 12px;
  border-radius: 11px;
  border: 1px solid transparent;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.nav-item:hover {
  background: rgba(255, 255, 255, 0.04);
}

.nav-item.active {
  background: linear-gradient(90deg, rgba(255, 154, 61, 0.12), rgba(255, 154, 61, 0.03));
  border-color: rgba(255, 154, 61, 0.16);
  box-shadow: inset 3px 0 0 var(--accent);
}

.nav-icon {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.045);
  color: var(--text-muted);
  flex-shrink: 0;
  transition: background 0.15s ease, color 0.15s ease;
}

.nav-item.active .nav-icon {
  background: var(--accent-soft);
  color: var(--accent);
}

.nav-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.nav-label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text);
}

.nav-desc {
  font-size: 10.5px;
  color: var(--text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sp-content {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 28px 36px 48px;
  display: flex;
  flex-direction: column;
}

/* ---------- 面板 ---------- */
.panel {
  max-width: 880px;
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 2px;
}

.panel-head h2 {
  margin: 0;
  font-size: 19px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.panel-head p {
  margin: 0;
  font-size: 12px;
  color: var(--text-faint);
  flex: 1;
  min-width: 200px;
  line-height: 1.6;
}

.card {
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.035), rgba(255, 255, 255, 0.012));
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 17px 19px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  box-shadow: var(--shadow-sm), var(--ring-inset);
  transition: border-color 0.18s ease;
}

.card:hover {
  border-color: var(--border-strong);
}

.card-title {
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-faint);
}

.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
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

textarea.inp {
  line-height: 1.6;
  resize: vertical;
}

.preset-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.chip {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: 999px;
  padding: 6px 14px;
  font-size: 11.5px;
  cursor: pointer;
  transition: all 0.15s ease;
}

.chip:hover {
  border-color: var(--border-accent);
  color: var(--text);
  background: rgba(255, 154, 61, 0.08);
}

.key-row {
  display: flex;
  gap: 12px;
  align-items: center;
}

.key-row .inp {
  flex: 1;
}

.chk {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--text-muted);
  cursor: pointer;
  flex-shrink: 0;
}

.chk input {
  accent-color: var(--accent);
}

.model-line {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.model-line .inp {
  flex: 1;
  min-width: 160px;
}

.test {
  font-size: 11.5px;
}

.test.ok {
  color: var(--ok);
}

.test.bad {
  color: var(--danger);
}

.note {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  font-size: 11.5px;
  color: var(--text-faint);
  background: rgba(255, 255, 255, 0.02);
  border: 1px dashed var(--border);
  border-radius: 10px;
  padding: 11px 14px;
  line-height: 1.6;
}

.note :deep(.ic) {
  margin-top: 2px;
  color: var(--accent);
}

.hint {
  font-size: 10.5px;
  color: var(--text-faint);
  line-height: 1.55;
}

/* 长期记忆编辑框 */
.memory-editor {
  margin-top: 10px;
  width: 100%;
  resize: vertical;
  font-family: inherit;
  line-height: 1.6;
}

.memory-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
}

.hint-inline {
  font-size: 10.5px;
  color: var(--text-faint);
}

/* 定时任务管理 */
/* ---------- 旧版本横幅 ---------- */
.legacy-banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 12px;
  border-color: rgba(250, 204, 21, 0.35);
  color: #facc15;
  font-size: 12px;
  line-height: 1.7;
}

.legacy-banner b {
  color: #fde68a;
}

.legacy-banner span {
  flex: 1;
}

/* ---------- 当前生效摘要条 ---------- */
.active-summary {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.active-summary .as-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.active-summary .as-main b {
  font-size: 13.5px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.active-summary .as-main i {
  font-style: normal;
  font-size: 11px;
  color: var(--text-faint);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.as-source {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  padding: 3px 10px;
}

.as-source.warn {
  color: #facc15;
  border-color: rgba(250, 204, 21, 0.35);
}

.adopt-btn {
  flex-shrink: 0;
  color: var(--accent);
}

.adopt-name {
  width: 240px;
  flex-shrink: 0;
}

.pr-chevron {
  flex-shrink: 0;
  display: flex;
  color: var(--text-faint);
  cursor: pointer;
  padding: 3px;
  border-radius: 6px;
  transition: color 0.15s ease, background 0.15s ease;
}

.pr-chevron:hover {
  color: var(--accent);
  background: rgba(255, 154, 61, 0.08);
}

/* ---------- 空态收编按钮 ---------- */
.eb-action {
  margin-top: 10px;
}

/* ---------- 模型配置档案 ---------- */
.custom-flag {
  margin-left: 10px;
  font-size: 10.5px;
  font-weight: 400;
  color: #facc15;
  border: 1px solid rgba(250, 204, 21, 0.35);
  border-radius: 999px;
  padding: 1px 8px;
  vertical-align: middle;
}

.profile-item {
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-top: 8px;
  overflow: hidden;
  transition: border-color 0.15s ease;
}

.profile-item.active {
  border-color: rgba(255, 154, 61, 0.4);
  background: linear-gradient(135deg, rgba(255, 154, 61, 0.05), transparent);
}

.ab-btn.primary.used {
  background: rgba(255, 255, 255, 0.05);
  color: var(--accent);
  box-shadow: none;
  cursor: default;
}

.profile-row {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 12px;
  cursor: pointer;
}

.profile-item.open .profile-row {
  border-bottom: 1px solid var(--border);
}

.pr-badge {
  flex-shrink: 0;
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 999px;
  border: 1px solid var(--border-strong);
  color: var(--text-muted);
}

.pr-badge.mock {
  border-color: rgba(250, 204, 21, 0.4);
  color: #facc15;
}

.pr-badge.openai {
  border-color: rgba(74, 222, 128, 0.35);
  color: #4ade80;
}

.pr-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.pr-main b {
  font-size: 12.5px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pr-main i {
  font-style: normal;
  font-size: 10.5px;
  color: var(--text-faint);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.profile-edit {
  display: flex;
  flex-direction: column;
  gap: 7px;
  padding: 10px 12px 12px;
}

.pe-row {
  display: flex;
  gap: 7px;
}

.pe-name {
  flex: 1;
}

.pe-mode {
  width: 170px;
  flex-shrink: 0;
}

.pe-presets {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.pe-foot {
  margin-top: 2px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.pe-foot .hint {
  flex: 1;
  min-width: 0;
}

/* ---------- 权限规则 & 斜杠命令 ---------- */
.rule-row,
.cmd-row {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-bottom: 7px;
}

.rule-tool {
  width: 34%;
  flex-shrink: 0;
}

.rule-pattern {
  flex: 1;
}

.rule-effect {
  width: 96px;
  flex-shrink: 0;
}

.cmd-name {
  width: 180px;
  flex-shrink: 0;
  font-family: var(--font-mono);
}

.cmd-template {
  flex: 1;
  resize: vertical;
}

.auto-list {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.auto-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
}
.auto-row.off { opacity: 0.55; }
.auto-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 13px;
}
.auto-main code {
  font-size: 11.5px;
  color: var(--accent);
}
.auto-ops { display: flex; gap: 6px; }
.auto-new {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.auto-new .btn { align-self: flex-start; }

/* MCP */
.mcp-card {
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.03), rgba(255, 255, 255, 0.012));
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 9px;
  box-shadow: var(--shadow-sm), var(--ring-inset);
}

.mcp-card.off {
  opacity: 0.55;
}

.mcp-top {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.mcp-name {
  font-size: 12.5px;
  font-weight: 600;
  flex: 1;
  min-width: 100px;
}

.mcp-cmd {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-muted);
  background: rgba(0, 0, 0, 0.24);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 10px;
  word-break: break-all;
}

.mcp-args {
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.55;
  resize: vertical;
}

.mcp-status {
  display: flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
  font-size: 11.5px;
}

.mcp-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 8px currentColor;
  flex-shrink: 0;
}

.mcp-status.ok {
  color: var(--ok);
}

.mcp-status.bad {
  color: var(--danger);
}

.mcp-hint {
  color: var(--text-faint);
}

.mcp-ro {
  margin-left: auto;
  font-size: 9.5px;
  letter-spacing: 0.04em;
  color: var(--text-faint);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 1px 7px;
  background: rgba(255, 255, 255, 0.02);
}

.empty-box {
  font-size: 11.5px;
  color: var(--text-faint);
  padding: 13px 15px;
  border: 1px dashed var(--border);
  border-radius: 10px;
}

.add-row {
  display: flex;
  gap: 10px;
}

.add-row .inp {
  flex: 1;
}

.manual-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 9px;
}

.manual-grid .inp.wide {
  grid-column: 1 / -1;
}

/* 目录 */
.dir-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  background: rgba(0, 0, 0, 0.2);
  border: 1px solid var(--border);
  border-radius: 9px;
  padding: 9px 12px;
}

.dir-row code {
  font-size: 11px;
  word-break: break-all;
  color: var(--text-muted);
}

/* 按钮 */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid var(--border);
  border-radius: 9px;
  padding: 9px 14px;
  font-size: 12px;
  cursor: pointer;
  background: rgba(255, 255, 255, 0.04);
  color: var(--text);
  transition: all 0.15s ease;
  white-space: nowrap;
  text-decoration: none;
}

.backup-card {
  margin-top: 14px;
}

.backup-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 12px;
}

.check-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  cursor: pointer;
}

.check-row input[type='checkbox'] {
  width: 15px;
  height: 15px;
  accent-color: var(--accent);
  cursor: pointer;
}

/* ---------- 回收站 ---------- */
.trash-card {
  margin-top: 14px;
}

.trash-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.trash-empty {
  margin-top: 12px;
  padding: 14px;
  border: 1px dashed var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-faint);
  font-size: 12px;
  text-align: center;
}

.trash-list {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 批量操作栏 */
.trash-batch {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: rgba(255, 255, 255, 0.02);
}

.tb-all {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
  cursor: pointer;
  user-select: none;
}

.tb-count {
  flex: 1;
  font-size: 11px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.tb-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.trash-batch input[type='checkbox'],
.ti-check input[type='checkbox'] {
  width: 14px;
  height: 14px;
  accent-color: var(--accent);
  cursor: pointer;
}

.ti-check {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  cursor: pointer;
}

.trash-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-inset);
  transition: border-color 0.15s ease, background 0.15s ease;
}

.trash-item.selected {
  border-color: rgba(255, 154, 61, 0.35);
  background: var(--accent-soft);
}

.ti-main {
  flex: 1;
  min-width: 0;
}

.ti-title {
  font-size: 12.5px;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ti-meta {
  margin-top: 3px;
  font-size: 10.5px;
  color: var(--text-faint);
  font-variant-numeric: tabular-nums;
}

.ti-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.btn.primary {
  background: var(--accent-gradient);
  border-color: transparent;
  color: var(--accent-text);
  font-weight: 700;
  box-shadow: var(--shadow-accent);
}

.btn.primary:hover:not(:disabled) {
  filter: brightness(1.04);
  transform: translateY(-1px);
}

.btn.primary:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.btn.ghost {
  background: transparent;
}

.btn.ghost:hover:not(:disabled) {
  border-color: var(--border-strong);
  background: rgba(255, 255, 255, 0.05);
}

.btn.tiny {
  padding: 6px 10px;
  font-size: 11px;
}

.btn.danger-ghost {
  background: transparent;
  border-color: transparent;
  color: var(--text-faint);
}

.btn.danger-ghost:hover {
  color: var(--danger);
  border-color: rgba(251, 113, 133, 0.25);
  background: var(--danger-soft);
}

.btn.danger {
  background: transparent;
  border-color: rgba(251, 113, 133, 0.3);
  color: var(--danger);
}

.btn.danger:hover:not(:disabled) {
  background: var(--danger-soft);
}

.err-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--danger);
  font-size: 12px;
  padding: 8px 0;
}

/* Toast */
.toast {
  position: fixed;
  top: 78px;
  left: 50%;
  transform: translateX(-50%);
  background: rgba(74, 222, 128, 0.12);
  border: 1px solid rgba(74, 222, 128, 0.28);
  color: var(--ok);
  font-weight: 600;
  font-size: 12px;
  padding: 10px 22px;
  border-radius: 999px;
  box-shadow: var(--shadow);
  backdrop-filter: blur(18px);
  z-index: 60;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(-8px);
}

@media (max-width: 900px) {
  .sp-nav {
    width: 76px;
    padding: 16px 10px;
  }

  .nav-text {
    display: none;
  }

  .sp-content {
    padding: 22px 18px 36px;
  }

  .grid {
    grid-template-columns: 1fr;
  }

  .manual-grid {
    grid-template-columns: 1fr;
  }
}
</style>
