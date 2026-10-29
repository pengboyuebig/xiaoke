// 与后端共享的类型定义

export type Role = 'user' | 'assistant' | 'tool'

export interface ToolCallInfo {
  id: string
  name: string
  arguments: string
}

export interface Message {
  role: Role
  content: string
  createdAt?: string
  /** 仅前端本地：用户中途停止生成，服务端未保存这条半截回复 */
  stopped?: boolean
  /** assistant 消息携带的工具调用 */
  toolCalls?: ToolCallInfo[]
  /** role=tool 时：对应调用的 ID 与工具名 */
  toolCallId?: string
  name?: string
}

/** 流式过程中的工具活动（live 状态，落库后由服务端消息接管） */
export interface ToolActivity {
  name: string
  args?: string
  done: boolean
  ok?: boolean
  preview?: string
  error?: string
  ms?: number
}

/** 同一回合的工具调用与结果（按 toolCallId 配对） */
export interface ToolPair {
  id: string
  name: string
  arguments: string
  result?: string
  ok?: boolean
}

/** 前端把连续的 assistant/tool 消息合并后的渲染分组 */
export interface MsgGroup {
  /** assistant | live：带头像与工具区块；user：右对齐气泡 */
  kind: 'assistant' | 'user' | 'live'
  /** assistant 分组的正文章节（一条 assistant 消息一段） */
  sections: string[]
  /** 工具调用配对（assistant / live 分组用） */
  tools: ToolPair[]
  error?: string
  streaming?: boolean
  /** user 内容 */
  content?: string
}

export interface SessionMeta {
  id: string
  title: string
  projectId?: string
  agentId?: string
  createdAt: string
  updatedAt: string
  messageCount: number
}

export interface Session {
  id: string
  title: string
  projectId?: string
  agentId?: string
  createdAt: string
  updatedAt: string
  messages: Message[]
}

export interface ListResponse {
  sessions: SessionMeta[]
  greeting: string
  agentName: string
  emptyProjectHint?: string
}

export interface HealthInfo {
  ok: number
  agent: string
  mode: string
  model: string
}

// ---------- 项目 ----------

export interface Project {
  id: string
  name: string
  path: string
  template?: string
  createdAt: string
}

// ---------- 智能体 ----------

export interface Agent {
  id: string
  name: string
  description?: string
  avatar?: string // /assets/... 或 emoji
  systemPrompt?: string
  tools: boolean
  createdAt: string
  updatedAt: string
}

// ---------- 技能 ----------

export interface Skill {
  id: string
  name: string
  description: string
  content: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export interface TreeItem {
  path: string
  isDir: boolean
}

// ---------- 设置 ----------

export interface MCPServerCfg {
  name: string
  command: string
  args: string[]
  env?: Record<string, string>
  enabled: boolean
}

export interface SettingsView {
  llm: {
    mode: string
    baseURL: string
    apiKeySet: boolean
    apiKeyTail: string
    model: string
    temperature: number
    maxHistory: number
  }
  mcpServers: MCPServerCfg[]
  readDirs: string[]
  projectsRoot: string
  projectsRootEffective: string
  maxToolSteps: number
  commandAllowlist: string[]
}

export interface MCPStatus {
  name: string
  enabled: boolean
  running: boolean
  error?: string
  tools: string[]
  toolHint?: string
}

/** 保存设置的请求体（apiKey 空 = 保持不变） */
export interface SettingsPayload {
  llm: {
    mode: string
    baseURL: string
    apiKey: string
    model: string
    temperature: number
    maxHistory: number
  }
  mcpServers: MCPServerCfg[]
  readDirs: string[]
  projectsRoot: string
  maxToolSteps: number
  commandAllowlist: string[]
}
