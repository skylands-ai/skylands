import { el } from './dom'

/**
 * Renders the Markdown an agent writes as DOM nodes, never as HTML text, so
 * nothing in an agent's words can run in the page. It covers what replies
 * use: paragraphs, headings, bullet and numbered lists, tables, quotes,
 * rules, fenced code, inline code, bold, italic, and http links.
 */
export function renderMarkdown(src: string): DocumentFragment {
  const out = document.createDocumentFragment()
  const lines = src.replace(/\r\n?/g, '\n').split('\n')
  let i = 0
  const tableAt = (n: number): boolean => n + 1 < lines.length && lines[n].includes('|') && DELIM.test(lines[n + 1]) && cells(lines[n]).length === cells(lines[n + 1]).length
  while (i < lines.length) {
    const line = lines[i]
    if (line.trim() === '') {
      i++
      continue
    }
    const fence = /^\s*(```|~~~)\s*(\S*)/.exec(line)
    if (fence) {
      const code: string[] = []
      i++
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) code.push(lines[i++])
      i++
      const c = el('code', { text: code.join('\n') })
      if (fence[2]) c.setAttribute('data-lang', fence[2])
      out.appendChild(el('pre', {}, [c]))
      continue
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line)
    if (heading) {
      const level = Math.min(4, heading[1].length)
      out.appendChild(el(`h${level}` as 'h1', {}, inline(heading[2])))
      i++
      continue
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.appendChild(el('hr'))
      i++
      continue
    }
    const bullet = /^\s*[-*+]\s+/
    const number = /^\s*\d+[.)]\s+/
    if (bullet.test(line) || number.test(line)) {
      const ordered = number.test(line)
      const marker = ordered ? number : bullet
      const list = el(ordered ? 'ol' : 'ul')
      while (i < lines.length && marker.test(lines[i])) {
        let item = lines[i].replace(marker, '')
        i++
        // Continuation lines, indented, belong to the item.
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !marker.test(lines[i])) item += '\n' + lines[i++].trim()
        list.appendChild(el('li', {}, inline(item)))
      }
      out.appendChild(list)
      continue
    }
    if (/^\s*>/.test(line)) {
      const quoted: string[] = []
      while (i < lines.length && /^\s*>/.test(lines[i])) quoted.push(lines[i++].replace(/^\s*>\s?/, ''))
      out.appendChild(el('blockquote', {}, [renderMarkdown(quoted.join('\n'))]))
      continue
    }
    if (tableAt(i)) {
      const header = cells(lines[i])
      const aligns = cells(lines[i + 1]).map((d) => (d.startsWith(':') && d.endsWith(':') ? 'center' : d.endsWith(':') ? 'right' : d.startsWith(':') ? 'left' : ''))
      i += 2
      const cell = (tag: 'th' | 'td', text: string, col: number): HTMLElement => {
        const c = el(tag, {}, inline(text))
        if (aligns[col]) c.style.textAlign = aligns[col]
        return c
      }
      const body = el('tbody')
      while (i < lines.length && lines[i].trim() !== '' && lines[i].includes('|')) {
        const row = cells(lines[i++])
        body.appendChild(el('tr', {}, header.map((_, col) => cell('td', row[col] ?? '', col))))
      }
      const table = el('table', {}, [el('thead', {}, [el('tr', {}, header.map((h, col) => cell('th', h, col)))]), body])
      out.appendChild(el('div', { class: 'tablewrap' }, [table]))
      continue
    }
    const para: string[] = []
    while (i < lines.length && lines[i].trim() !== '' && !/^\s*(```|~~~|#{1,6}\s|[-*+]\s|\d+[.)]\s|>)/.test(lines[i]) && !tableAt(i)) para.push(lines[i++])
    out.appendChild(el('p', {}, inline(para.join('\n'))))
  }
  return out
}

/** The row under a table's header: dashes per column, with colons for alignment. */
const DELIM = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/

/** Splits a table row into cell texts. A pipe escaped as \| stays in its cell. */
function cells(row: string): string[] {
  let r = row.trim()
  if (r.startsWith('|')) r = r.slice(1)
  if (r.endsWith('|') && !r.endsWith('\\|')) r = r.slice(0, -1)
  return r.split(/(?<!\\)\|/).map((c) => c.replace(/\\\|/g, '|').trim())
}

const INLINE = /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)|\*\*([^*\n]+?)\*\*|__([^_\n]+?)__|(?<![\w*])\*([^*\n]+?)\*(?![\w*])|(?<![\w_])_([^_\n]+?)_(?![\w_])/

/** Inline Markdown as nodes: code spans, links, bold, italic. */
function inline(text: string): Node[] {
  const nodes: Node[] = []
  let rest = text
  for (;;) {
    const m = INLINE.exec(rest)
    if (!m) break
    if (m.index > 0) nodes.push(document.createTextNode(rest.slice(0, m.index)))
    if (m[2] !== undefined) nodes.push(el('code', { text: m[2].trim() }))
    else if (m[3] !== undefined) nodes.push(el('a', { href: m[4], target: '_blank', rel: 'noopener noreferrer' }, inline(m[3])))
    else if (m[5] !== undefined || m[6] !== undefined) nodes.push(el('strong', {}, inline(m[5] ?? m[6])))
    else nodes.push(el('em', {}, inline(m[7] ?? m[8])))
    rest = rest.slice(m.index + m[0].length)
  }
  if (rest) nodes.push(document.createTextNode(rest))
  return nodes
}
