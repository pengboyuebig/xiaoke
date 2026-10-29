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
