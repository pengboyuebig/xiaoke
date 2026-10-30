/**
 * 应用内确认框：替代原生 confirm()，避免系统弹窗打断视觉一致性。
 *
 * 用法（与 confirm 一致的 Promise<boolean> 语义）：
 *   if (!(await askConfirm({ title: '删除会话', message: '...' }))) return
 *
 * 单例挂载：任意位置调用都复用同一个弹窗实例，避免多个同时弹出。
 */
import { reactive, readonly } from 'vue'

export interface ConfirmOptions {
  title?: string
  message?: string
  /** 确认按钮文案，默认「确定」 */
  confirmText?: string
  /** 取消按钮文案，默认「取消」 */
  cancelText?: string
  /** 危险操作（删除类）：确认按钮用红色，默认 true */
  danger?: boolean
}

interface ConfirmState extends ConfirmOptions {
  open: boolean
}

const state = reactive<ConfirmState>({
  open: false,
  title: '',
  message: '',
  confirmText: '确定',
  cancelText: '取消',
  danger: true,
})

let resolver: ((ok: boolean) => void) | null = null

export const confirmState = readonly(state)

export function askConfirm(opts: ConfirmOptions): Promise<boolean> {
  // 已有弹窗未关闭时，先按「取消」结掉上一个，避免 Promise 悬挂
  if (resolver) {
    const prev = resolver
    resolver = null
    prev(false)
  }
  state.title = opts.title || '确认操作'
  state.message = opts.message || ''
  state.confirmText = opts.confirmText || '确定'
  state.cancelText = opts.cancelText || '取消'
  state.danger = opts.danger !== false
  state.open = true
  return new Promise<boolean>((resolve) => {
    resolver = resolve
  })
}

export function resolveConfirm(ok: boolean) {
  if (!resolver) return
  const r = resolver
  resolver = null
  state.open = false
  r(ok)
}
