import type {
  Agent,
  HealthInfo,
  ListResponse,
  MCPStatus,
  Message,
  Project,
  Session,
  SessionMeta,
  SettingsPayload,
  SettingsView,
  Skill,
  TrashItem,
  TreeItem,
} from '../types'

async function jfetch<T>(input: string, init?: RequestInit): Promise<T> {
  const res = await fetch(input, init)
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error((data as { error?: string }).error || `请求失败 (${res.status})`)
  }
  return data as T
}

function jsonBody(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

export interface ChatOptions {
  sessionId: string
  content: string
  projectId?: string
  agentId?: string
  /** 思考程度：none/minimal/low/medium/high（空=none，不注入请求体） */
  reasoning?: string
  /** 工作模式：plan（只读调研+计划）| build（开发） */
  mode?: string
  /** 批准计划：本条消息只驱动模型，不在对话中显示 */
  approve?: boolean
  signal?: AbortSignal
  onDelta: (text: string) => void
  onToolStart?: (name: string, args: string) => void
  onToolResult?: (info: { name: string; ok: boolean; preview: string; error?: string; ms: number }) => void
}

export const api = {
  // ---------- 会话 ----------
  listSessions(): Promise<ListResponse> {
    return jfetch('/api/sessions')
  },
  createSession(projectId?: string, agentId?: string): Promise<Session> {
    return jfetch('/api/sessions', jsonBody('POST', { projectId: projectId || '', agentId: agentId || '' }))
  },
  getSession(id: string): Promise<Session> {
    return jfetch(`/api/sessions/${id}`)
  },
  deleteSession(id: string): Promise<void> {
    return jfetch(`/api/sessions/${id}`, { method: 'DELETE' })
  },
  // ---------- 回收站 ----------
  listTrash(): Promise<{ items: TrashItem[] }> {
    return jfetch('/api/trash')
  },
  restoreTrash(dir: string): Promise<{ ok: string; id: string }> {
    return jfetch(`/api/trash/${encodeURIComponent(dir)}/restore`, { method: 'POST' })
  },
  purgeTrash(dir: string): Promise<{ ok: string }> {
    return jfetch(`/api/trash/${encodeURIComponent(dir)}`, { method: 'DELETE' })
  },
  health(): Promise<HealthInfo> {
    return jfetch('/healthz')
  },

  // ---------- 项目 ----------
  listProjects(): Promise<{ projects: Project[]; root: string }> {
    return jfetch('/api/projects')
  },
  createProject(name: string, template: string, root?: string): Promise<Project> {
    return jfetch('/api/projects', jsonBody('POST', { name, template, root: root || '' }))
  },
  deleteProject(id: string): Promise<void> {
    return jfetch(`/api/projects/${id}`, { method: 'DELETE' })
  },
  projectTree(id: string): Promise<{ tree: TreeItem[] }> {
    return jfetch(`/api/projects/${id}/tree`)
  },

  // ---------- 智能体 ----------
  listAgents(): Promise<{ agents: Agent[] }> {
    return jfetch('/api/agents')
  },
  createAgent(a: Partial<Agent>): Promise<Agent> {
    return jfetch('/api/agents', jsonBody('POST', a))
  },
  updateAgent(id: string, a: Partial<Agent>): Promise<Agent> {
    return jfetch(`/api/agents/${id}`, jsonBody('PUT', a))
  },
  deleteAgent(id: string): Promise<void> {
    return jfetch(`/api/agents/${id}`, { method: 'DELETE' })
  },
  toggleAgentTools(id: string): Promise<Agent> {
    return jfetch(`/api/agents/${id}/toggle`, { method: 'POST' })
  },

  // ---------- 技能 ----------
  listSkills(): Promise<{ skills: Skill[] }> {
    return jfetch('/api/skills')
  },
  createSkill(s: Partial<Skill>): Promise<Skill> {
    return jfetch('/api/skills', jsonBody('POST', s))
  },
  updateSkill(id: string, s: Partial<Skill>): Promise<Skill> {
    return jfetch(`/api/skills/${id}`, jsonBody('PUT', s))
  },
  deleteSkill(id: string): Promise<void> {
    return jfetch(`/api/skills/${id}`, { method: 'DELETE' })
  },
  toggleSkill(id: string): Promise<Skill> {
    return jfetch(`/api/skills/${id}/toggle`, { method: 'POST' })
  },

  // ---------- 头像上传 ----------
  async uploadAvatar(file: File): Promise<{ url: string }> {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch('/api/upload/avatar', { method: 'POST', body: fd })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) {
      throw new Error((data as { error?: string }).error || `上传失败 (${res.status})`)
    }
    return data as { url: string }
  },

  // ---------- 设置 ----------
  getSettings(): Promise<SettingsView> {
    return jfetch('/api/settings')
  },
  saveSettings(p: SettingsPayload, clearApiKey?: boolean): Promise<SettingsView> {
    const q = clearApiKey ? '?clearApiKey=true' : ''
    return jfetch(`/api/settings${q}`, jsonBody('PUT', p))
  },
  testLLM(t: { baseURL?: string; apiKey?: string; model?: string }): Promise<{ ok: boolean; error?: string; models?: string[] }> {
    return jfetch('/api/settings/test', jsonBody('POST', t))
  },
  listModels(): Promise<{ models: string[]; error?: string }> {
    return jfetch('/api/models')
  },
  mcpStatus(): Promise<{ servers: MCPStatus[] }> {
    return jfetch('/api/mcp/status')
  },
  /** 收敛 MCP 进程：退出/启动失败的 server 会重新拉起 */
  retryMCP(): Promise<{ servers: MCPStatus[] }> {
    return jfetch('/api/mcp/retry', { method: 'POST' })
  },

  /**
   * 发起一次流式对话（含工具调用循环）。onDelta 逐段接收文本增量；
   * onToolStart / onToolResult 上报工具活动。通过 signal 可中断。
   */
  chat(opts: ChatOptions): Promise<{ sessionId: string }> {
    return new Promise((resolve, reject) => {
      fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sessionId: opts.sessionId,
          content: opts.content,
          projectId: opts.projectId || '',
          agentId: opts.agentId || '',
          reasoning: opts.reasoning || '',
          mode: opts.mode || 'build',
          approve: opts.approve === true,
        }),
        signal: opts.signal,
      })
        .then(async (res) => {
          if (!res.ok || !res.body) {
            const data = await res.json().catch(() => ({}))
            throw new Error((data as { error?: string }).error || `请求失败 (${res.status})`)
          }
          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          let buffer = ''
          let sessionId = opts.sessionId
          for (;;) {
            const { done, value } = await reader.read()
            if (done) break
            buffer += decoder.decode(value, { stream: true })
            // SSE 帧以空行分隔；最后一段可能不完整，留在 buffer
            const frames = buffer.split('\n\n')
            buffer = frames.pop() ?? ''
            for (const frame of frames) {
              for (const line of frame.split('\n')) {
                if (!line.startsWith('data:')) continue
                const payload = line.slice(5).trim()
                if (!payload) continue
                let evt: Record<string, unknown>
                try {
                  evt = JSON.parse(payload)
                } catch {
                  continue // 坏帧忽略
                }
                switch (evt.type) {
                  case 'start':
                    sessionId = evt.sessionId as string
                    break
                  case 'delta':
                    opts.onDelta(evt.text as string)
                    break
                  case 'tool':
                    opts.onToolStart?.(evt.name as string, (evt.args as string) || '')
                    break
                  case 'tool_result':
                    opts.onToolResult?.({
                      name: evt.name as string,
                      ok: evt.ok as boolean,
                      preview: (evt.preview as string) || '',
                      error: evt.error as string | undefined,
                      ms: (evt.ms as number) || 0,
                    })
                    break
                  case 'error':
                    throw new Error((evt.error as string) || '未知错误')
                  case 'done':
                    resolve({ sessionId })
                    return
                }
              }
            }
          }
          resolve({ sessionId })
        })
        .catch((err) => {
          if (err && err.name === 'AbortError') {
            resolve({ sessionId: opts.sessionId })
            return
          }
          // 连接级失败（服务重启/断网）给出可读提示，原始信息是 "Failed to fetch" / "network error"
          const msg = String((err && err.message) || err || '')
          if (err instanceof TypeError || /failed to fetch|network\s*error|load failed/i.test(msg)) {
            reject(new Error('连接中断（服务可能已重启或网络异常），请重试'))
            return
          }
          reject(err)
        })
    })
  },
}

/** 会话消息的工具调用回执（供渲染） */
export type { Message, SessionMeta }
