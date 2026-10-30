import { Marked } from 'marked'
import DOMPurify from 'dompurify'

const marked = new Marked({
  gfm: true,
  breaks: true,
})

/** Markdown -> 安全 HTML（marked 解析 + DOMPurify 消毒，与 opensquilla 渲染管线同思路） */
export function renderMarkdown(src: string): string {
  const raw = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(raw, {
    ADD_ATTR: ['target'],
  })
}

/** 超过该行数的代码块默认折叠（只显示语言+行数摘要） */
const COLLAPSE_MIN_LINES = 10

/**
 * 渲染 Markdown，并把长代码块折叠成一行摘要。
 * 用于助手答复：长代码/命令默认收起，只保留步骤标题，点击摘要可展开。
 */
export function renderMarkdownCollapsed(src: string): string {
  const safe = renderMarkdown(src)
  if (typeof DOMParser === 'undefined') return safe

  const doc = new DOMParser().parseFromString(safe, 'text/html')
  let changed = false
  for (const pre of Array.from(doc.querySelectorAll('pre'))) {
    const code = pre.querySelector('code')
    const text = (code?.textContent ?? pre.textContent ?? '').replace(/\n$/, '')
    if (!text) continue
    const lines = text.split('\n').length
    if (lines < COLLAPSE_MIN_LINES) continue

    const lang = code?.className.match(/language-([\w-]+)/)?.[1] || 'code'
    const details = doc.createElement('details')
    details.className = 'code-collapse'
    const summary = doc.createElement('summary')
    summary.textContent = `${lang} · ${lines} 行（点击展开）`
    details.appendChild(summary)
    details.appendChild(pre.cloneNode(true))
    pre.replaceWith(details)
    changed = true
  }
  return changed ? doc.body.innerHTML : safe
}
