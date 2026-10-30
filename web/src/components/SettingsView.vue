<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api } from '../lib/api'
import type { Agent, MCPServerCfg, MCPStatus, SettingsView, Skill } from '../types'
import AgentSection from './AgentSection.vue'
import SkillSection from './SkillSection.vue'

const emit = defineEmits<{ close: [] }>()

type Tab = 'agents' | 'skills' | 'llm' | 'mcp' | 'dirs' | 'adv'
const tab = ref<Tab>('llm')
const loading = ref(true)
const saving = ref(false)
const saveErr = ref('')
const toast = ref('')

const TABS: { id: Tab; icon: string; label: string; desc: string }[] = [
  { id: 'agents', icon: '🤖', label: '智能体', desc: '名字、头像、人格' },
  { id: 'skills', icon: '📚', label: '技能', desc: '可复用的知识注入' },
  { id: 'llm', icon: '🧠', label: '模型', desc: 'API 与思考程度' },
  { id: 'mcp', icon: '🔌', label: 'MCP', desc: '外部工具服务' },
  { id: 'dirs', icon: '📁', label: '目录', desc: '只读授权与项目根' },
  { id: 'adv', icon: '⚙️', label: '高级', desc: '轮数与命令白名单' },
]

// ---------- 数据 ----------
const agents = ref<Agent[]>([])
const skills = ref<Skill[]>([])
const mcpStatuses = ref<MCPStatus[]>([])

const form = reactive({
  mode: 'mock',
  baseURL: '',
  apiKey: '',
  clearApiKey: false,
  model: '',
  temperature: 0.7,
  maxHistory: 20,
  readDirs: [] as string[],
  newReadDir: '',
  projectsRoot: '',
  maxToolSteps: 8,
  allowlistText: 'git, npm, node, python, pip',
  mcpServers: [] as (MCPServerCfg & { argLines: string })[],
})
const newMcp = reactive({ name: '', command: '', argsText: '', dir: '' })
const keyInfo = reactive({ set: false, tail: '' })
const testResult = ref<{ ok: boolean; text: string } | null>(null)
const modelList = ref<string[]>([])
const pullingModels = ref(false)
const testing = ref(false)

const tabMeta = computed(() => TABS.find((t) => t.id === tab.value))

onMounted(async () => {
  await Promise.all([loadSettings(), refreshAgents(), refreshSkills(), refreshMcpStatus()])
  loading.value = false
})

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
    form.allowlistText = s.commandAllowlist.join(', ')
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

function showToast(msg: string) {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2200)
}

// ---------- 模型区 ----------
function applyPreset(preset: 'deepseek' | 'gateway' | 'zen') {
  form.mode = 'openai'
  if (preset === 'deepseek') {
    form.baseURL = 'https://api.deepseek.com'
    form.model = 'deepseek-chat'
  } else if (preset === 'gateway') {
    form.baseURL = 'http://localhost:3000/api/v1'
    form.model = ''
  } else {
    form.baseURL = 'https://opencode.ai/zen/v1'
    form.model = ''
  }
  testResult.value = null
}

async function runTest() {
  testing.value = true
  testResult.value = null
  try {
    const r = await api.testLLM({ baseURL: form.baseURL, apiKey: form.apiKey || undefined, model: form.model })
    if (r.ok) {
      modelList.value = r.models || []
      testResult.value = { ok: true, text: `连接成功，发现 ${modelList.value.length} 个模型` }
    } else {
      testResult.value = { ok: false, text: r.error || '连接失败' }
    }
  } catch (e) {
    testResult.value = { ok: false, text: (e as Error).message }
  } finally {
    testing.value = false
  }
}

async function pullModels() {
  pullingModels.value = true
  try {
    // 用表单里的当前值测试/拉取，而不是已保存的旧配置
    const r = await api.testLLM({ baseURL: form.baseURL, apiKey: form.apiKey || undefined, model: form.model })
    modelList.value = r.models || []
    if (r.ok) {
      testResult.value = { ok: true, text: `拉取成功，发现 ${modelList.value.length} 个模型` }
    } else {
      testResult.value = { ok: false, text: r.error || '拉取失败' }
    }
  } catch (e) {
    testResult.value = { ok: false, text: (e as Error).message }
  } finally {
    pullingModels.value = false
  }
}

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
async function save() {
  saving.value = true
  saveErr.value = ''
  try {
    await api.saveSettings(
      {
        llm: {
          mode: form.mode,
          baseURL: form.baseURL.trim(),
          apiKey: form.apiKey,
          model: form.model.trim(),
          temperature: Number(form.temperature) || 0.7,
          maxHistory: Number(form.maxHistory) || 20,
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
        maxToolSteps: Number(form.maxToolSteps) || 8,
        commandAllowlist: form.allowlistText.split(/[,,\n]/).map((s) => s.trim()).filter(Boolean),
      },
      form.clearApiKey,
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
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M19 12H5M12 19l-7-7 7-7" />
        </svg>
        返回对话
      </button>
      <div class="sp-title">
        <span class="sp-icon">⚙️</span>
        <div>
          <div class="sp-name">设置中心</div>
          <div class="sp-desc">{{ tabMeta?.desc }}</div>
        </div>
      </div>
      <button class="save-btn" :disabled="saving" @click="save">
        {{ saving ? '保存中…' : '💾 保存并应用' }}
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
          <span class="nav-icon">{{ t.icon }}</span>
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
            <p>任意 OpenAI 兼容上游；改完点右上角保存，热生效。</p>
          </div>
          <div class="grid">
            <div class="card">
              <div class="card-title">运行模式</div>
              <select v-model="form.mode" class="inp">
                <option value="mock">mock 演示（免 Key）</option>
                <option value="openai">OpenAI 兼容上游</option>
              </select>
            </div>
            <div class="card">
              <div class="card-title">快捷预设</div>
              <div class="preset-row">
                <button class="chip" @click="applyPreset('deepseek')">DeepSeek 官方</button>
                <button class="chip" @click="applyPreset('gateway')">本地 opencode 网关</button>
                <button class="chip" @click="applyPreset('zen')">OpenCode Zen</button>
              </div>
            </div>
          </div>

          <template v-if="form.mode === 'openai'">
            <div class="card">
              <div class="card-title">Base URL</div>
              <input v-model="form.baseURL" class="inp" placeholder="https://api.deepseek.com 或 http://localhost:3000/api/v1" />
            </div>
            <div class="card">
              <div class="card-title">API Key</div>
              <div class="key-row">
                <input v-model="form.apiKey" class="inp" type="password" autocomplete="off"
                  :placeholder="keyInfo.set ? `已配置（尾号 ${keyInfo.tail}），留空保持不变` : 'sk-… / oc-…'" />
                <label class="chk"><input type="checkbox" v-model="form.clearApiKey" /> 清除</label>
              </div>
            </div>
            <div class="card">
              <div class="card-title">模型</div>
              <div class="model-line">
                <input v-model="form.model" class="inp" list="model-options" placeholder="deepseek-chat" />
                <datalist id="model-options">
                  <option v-for="m in modelList" :key="m" :value="m" />
                </datalist>
                <button class="btn ghost" :disabled="pullingModels" @click="pullModels">
                  {{ pullingModels ? '拉取中…' : '拉取列表' }}
                </button>
                <button class="btn ghost" :disabled="testing" @click="runTest">{{ testing ? '测试中…' : '测试连接' }}</button>
              </div>
              <div v-if="testResult" class="test" :class="testResult.ok ? 'ok' : 'bad'">{{ testResult.text }}</div>
            </div>
            <div class="grid">
              <div class="card">
                <div class="card-title">温度</div>
                <input v-model="form.temperature" class="inp" type="number" step="0.1" min="0" max="2" />
              </div>
              <div class="card">
                <div class="card-title">历史条数上限</div>
                <input v-model="form.maxHistory" class="inp" type="number" min="4" max="100" />
              </div>
            </div>
          </template>
          <div class="note">💡 思考程度在对话页右下角随时调节，无需进设置。</div>
        </section>

        <!-- MCP -->
        <section v-else-if="tab === 'mcp'" class="panel">
          <div class="panel-head">
            <h2>MCP 服务</h2>
            <p>每个服务是一个本机常驻进程（stdio）。参数每行一个，路径里的空格会原样保留。</p>
            <button class="btn ghost tiny" @click="refreshMcpStatus">↻ 刷新并重试</button>
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
              {{ statusOf(m.name)!.running ? `● 运行中 · ${statusOf(m.name)!.tools.length} 个工具` : `● ${statusOf(m.name)!.error || (statusOf(m.name)!.starting ? '启动中…' : '未运行')}` }}
              <span v-if="statusOf(m.name)!.toolHint">（{{ statusOf(m.name)!.toolHint }}）</span>
              <span v-if="m.readOnly" class="mcp-ro">· 只读过滤已开启</span>
            </div>
          </div>
          <div v-if="!form.mcpServers.length" class="empty-box">还没有 MCP 服务，用下面的方式添加。</div>

          <div class="card">
            <div class="card-title">一键添加 filesystem 服务（默认只读）</div>
            <div class="add-row">
              <input v-model="newMcp.dir" class="inp" placeholder="授权查看的目录绝对路径，如 D:\code\my-project" />
              <button class="btn primary" @click="addFilesystemServer">＋ 添加</button>
            </div>
            <div class="hint">官方 filesystem server 本身可读写；默认勾选「只读」后，write/edit/create/move/delete 类工具不会暴露给模型。</div>
          </div>
          <div class="card">
            <div class="card-title">手动添加其它 MCP 服务</div>
            <div class="manual-grid">
              <input v-model="newMcp.name" class="inp" placeholder="名称，如 git-tools" />
              <input v-model="newMcp.command" class="inp" placeholder="命令，如 npx / python / node" />
              <textarea v-model="newMcp.argsText" class="inp wide" rows="3" placeholder="参数，每行一个（路径可含空格）"></textarea>
              <button class="btn ghost" @click="addManualServer">添加</button>
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
        <section v-else class="panel">
          <div class="panel-head">
            <h2>高级</h2>
            <p>工具循环与命令执行的边界控制。</p>
          </div>
          <div class="grid">
            <div class="card">
              <div class="card-title">工具循环最大轮数</div>
              <input v-model="form.maxToolSteps" class="inp" type="number" min="1" max="32" />
              <div class="hint">模型单次回复最多连续调用工具的次数，超限强制收尾。</div>
            </div>
            <div class="card">
              <div class="card-title">命令白名单（逗号分隔）</div>
              <input v-model="form.allowlistText" class="inp" placeholder="git, npm, node, python, pip" />
              <div class="hint">run_command 只允许执行这里的命令，argv 直传不经 shell。</div>
            </div>
          </div>
        </section>

        <div v-if="saveErr" class="err-bar">⚠ {{ saveErr }}</div>
      </main>
    </div>

    <transition name="toast">
      <div v-if="toast" class="toast">{{ toast }}</div>
    </transition>
  </div>
</template>

<style scoped>
.settings-page {
  position: fixed;
  inset: 0;
  background: var(--bg);
  z-index: 40;
  display: flex;
  flex-direction: column;
}

/* ---------- 头部 ---------- */
.sp-head {
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 14px 26px;
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.back {
  display: flex;
  align-items: center;
  gap: 6px;
  background: var(--bg-surface-2);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  padding: 7px 14px;
  font-size: 13px;
  cursor: pointer;
  transition: all 0.15s ease;
}

.back:hover {
  color: var(--text);
  border-color: var(--accent);
}

.sp-title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
}

.sp-icon {
  font-size: 24px;
}

.sp-name {
  font-size: 16px;
  font-weight: 700;
}

.sp-desc {
  font-size: 12px;
  color: var(--text-faint);
}

.save-btn {
  background: linear-gradient(135deg, var(--accent), var(--accent-hover));
  color: var(--accent-text);
  border: none;
  border-radius: var(--radius-sm);
  padding: 9px 20px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 2px 10px rgba(242, 106, 27, 0.35);
  transition: transform 0.12s ease, box-shadow 0.12s ease;
}

.save-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 4px 16px rgba(242, 106, 27, 0.45);
}

.save-btn:disabled {
  opacity: 0.6;
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
  width: 210px;
  flex-shrink: 0;
  padding: 18px 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  border-right: 1px solid var(--border);
  background: var(--bg-surface);
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 10px 13px;
  border-radius: var(--radius);
  border: 1px solid transparent;
  background: none;
  cursor: pointer;
  text-align: left;
  transition: background 0.13s ease, border-color 0.13s ease;
}

.nav-item:hover {
  background: var(--bg-surface-2);
}

.nav-item.active {
  background: var(--accent-soft);
  border-color: color-mix(in srgb, var(--accent) 35%, transparent);
}

.nav-icon {
  font-size: 17px;
  flex-shrink: 0;
}

.nav-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.nav-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}

.nav-desc {
  font-size: 11px;
  color: var(--text-faint);
}

.nav-item.active .nav-label {
  color: var(--accent);
}

.sp-content {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 26px 32px 40px;
  display: flex;
  flex-direction: column;
}

/* ---------- 面板 ---------- */
.panel {
  max-width: 860px;
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
  margin-bottom: 4px;
}

.panel-head h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
}

.panel-head p {
  margin: 0;
  font-size: 12px;
  color: var(--text-faint);
  flex: 1;
}

.card {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 9px;
}

.card-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  letter-spacing: 0.4px;
}

.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.inp {
  background: var(--bg);
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

.preset-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.chip {
  background: var(--bg-surface-2);
  border: 1px solid var(--border);
  color: var(--text-muted);
  border-radius: 999px;
  padding: 5px 14px;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.13s ease;
}

.chip:hover {
  border-color: var(--accent);
  color: var(--text);
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
  font-size: 12px;
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
}

.model-line .inp {
  flex: 1;
}

.test {
  font-size: 12px;
}

.test.ok {
  color: var(--ok);
}

.test.bad {
  color: var(--danger);
}

.note {
  font-size: 12px;
  color: var(--text-faint);
  background: var(--bg-surface);
  border: 1px dashed var(--border);
  border-radius: var(--radius-sm);
  padding: 10px 14px;
}

.hint {
  font-size: 11px;
  color: var(--text-faint);
  line-height: 1.5;
}

/* MCP */
.mcp-card {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 13px 16px;
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.mcp-card.off {
  opacity: 0.6;
}

.mcp-top {
  display: flex;
  align-items: center;
  gap: 12px;
}

.mcp-name {
  font-size: 13px;
  flex: 1;
}

.mcp-cmd {
  font-size: 12px;
  color: var(--text-muted);
  background: var(--bg-inset);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 7px 10px;
  word-break: break-all;
}

.mcp-args {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  resize: vertical;
}

.mcp-ro {
  color: var(--text-faint);
  margin-left: 6px;
}

.mcp-status {
  font-size: 12px;
}

.mcp-status.ok {
  color: var(--ok);
}

.mcp-status.bad {
  color: var(--danger);
}

.empty-box {
  font-size: 12px;
  color: var(--text-faint);
  padding: 12px 14px;
  border: 1px dashed var(--border);
  border-radius: var(--radius-sm);
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
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 9px 12px;
}

.dir-row code {
  font-size: 12px;
  word-break: break-all;
}

/* 按钮 */
.btn {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 8px 15px;
  font-size: 13px;
  cursor: pointer;
  background: var(--bg-surface-2);
  color: var(--text);
  transition: all 0.13s ease;
  white-space: nowrap;
}

.btn.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--accent-text);
  font-weight: 600;
}

.btn.primary:hover:not(:disabled) {
  background: var(--accent-hover);
}

.btn.primary:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn.ghost {
  background: transparent;
}

.btn.ghost:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}

.btn.tiny {
  padding: 4px 11px;
  font-size: 12px;
}

.btn.danger-ghost {
  background: transparent;
  border-color: transparent;
  color: var(--text-faint);
}

.btn.danger-ghost:hover {
  color: var(--danger);
  border-color: var(--danger);
}

.btn.danger {
  background: transparent;
  border-color: color-mix(in srgb, var(--danger) 50%, transparent);
  color: var(--danger);
}

.btn.danger:hover:not(:disabled) {
  background: rgba(255, 107, 107, 0.1);
}

.err-bar {
  color: var(--danger);
  font-size: 13px;
  padding: 8px 0;
}

/* Toast */
.toast {
  position: fixed;
  top: 70px;
  left: 50%;
  transform: translateX(-50%);
  background: var(--ok);
  color: #06281c;
  font-weight: 600;
  font-size: 13px;
  padding: 9px 22px;
  border-radius: 999px;
  box-shadow: var(--shadow);
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

@media (max-width: 860px) {
  .sp-nav {
    width: 64px;
  }
  .nav-text {
    display: none;
  }
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
