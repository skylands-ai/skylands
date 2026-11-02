import { el } from './dom'
import type { FetchCache, Fetched } from './fetchcache'
import { timeNode } from './panels/parts'
import type { Session, ToolCallContent, ToolCallDetail } from './protocol'

/**
 * A tool call in a conversation is a chip where it happened, with the
 * title the transcript's line gives it. Behind the chip is the call's
 * record on the host: the exact input, what came back, and when. It is
 * fetched when the chip is opened and kept only while someone looks.
 */

/** The chips a reader has opened, by host, session, and tool call id, and a counter so a rebuild notices a toggle. */
const opened = new Set<string>()
let toggles = 0

const prefix = (x: Session): string => `${x.host}/${x.id}/`

/** What a rebuild of the conversation must notice: the opened chips and what was fetched for them. */
export function chipDeps(x: Session, cache: FetchCache): unknown[] {
  const deps: unknown[] = [toggles]
  const p = prefix(x)
  for (const k of opened) {
    if (!k.startsWith(p)) continue
    const f = cache.peek('toolcall', x.host, x.id, k.slice(p.length))
    deps.push(k, f?.at, f?.loading)
  }
  return deps
}

/** Fetches again the opened chips of calls still running while the turn runs, so their output grows. Called on a timer. */
export function refreshOpenChips(x: Session, cache: FetchCache): void {
  if (x.status !== 'running') return
  const p = prefix(x)
  for (const k of opened) {
    if (!k.startsWith(p)) continue
    const id = k.slice(p.length)
    const f = cache.peek('toolcall', x.host, x.id, id)
    if (f?.tool && !finished(f.tool.status)) cache.get('toolcall', x.host, x.id, 2000, 0, id)
  }
}

const finished = (status: string): boolean => status === 'completed' || status === 'failed'

/**
 * A tool call as a chip, and under it, once opened, its record. A chip
 * from a transcript written before calls were named has nothing behind it.
 */
export function toolChip(x: Session, title: string, id: string | undefined, cache: FetchCache, refresh: () => void): Node[] {
  if (!id) return [el('span', { class: 'toolchip', text: title })]
  const k = prefix(x) + id
  const isOpen = opened.has(k)
  const button = el('button', {
    type: 'button',
    class: 'toolchip',
    'aria-expanded': isOpen ? 'true' : 'false',
    title: isOpen ? 'Hide what this call did' : 'Show what this call did',
    text: title,
    onclick: () => {
      if (opened.has(k)) opened.delete(k)
      else opened.add(k)
      toggles++
      refresh()
    },
  })
  if (!isOpen) return [button]
  // The record is read once, and again after each event in the session,
  // such as the call finishing.
  const f = cache.get('toolcall', x.host, x.id, 0, Date.parse(x.updatedAt), id)
  return [button, detailPanel(title, f)]
}

/** The record as a panel: what kind of call, how it went, its input, and its output. */
function detailPanel(label: string, f: Fetched): HTMLElement {
  const box = el('div', { class: 'tooldetail', role: 'group', 'aria-label': `What ${label} did` })
  if (f.error) {
    box.appendChild(el('p', { class: 'sub bad', text: f.error }))
    return box
  }
  const d = f.tool
  if (!d) {
    box.appendChild(el('p', { class: 'sub muted', text: 'Fetching from the host…' }))
    return box
  }
  const row = el('div', { class: 'row' })
  if (d.kind) row.appendChild(el('span', { class: 'kindchip', text: d.kind }))
  row.appendChild(el('span', { class: `state ${d.status === 'failed' ? 'bad' : finished(d.status) ? 'ok' : ''}`, text: stateText(d) }))
  if (d.startedAt) row.appendChild(el('span', { class: 'muted' }, ['started ', timeNode(d.startedAt, 'ago')]))
  if (d.endedAt) row.appendChild(el('span', { class: 'muted', text: `took ${took(d.startedAt, d.endedAt)}` }))
  box.appendChild(row)
  // The line shows the title on one line and cut short; the whole of it
  // goes here when that lost something.
  const full = d.title ?? ''
  if (full && full.split(/\s+/).join(' ') !== label) box.appendChild(el('pre', { class: 'title', text: full }))
  if (d.locations?.length) box.appendChild(el('div', { class: 'field' }, [el('span', { class: 'key', text: d.locations.length === 1 ? 'file' : 'files' }), el('code', { text: d.locations.join(', ') })]))
  for (const n of inputFields(d.input)) box.appendChild(n)
  const out = outputBlocks(d)
  if (out.length) {
    box.appendChild(el('div', { class: 'field' }, [el('span', { class: 'key', text: 'output' }), ...out]))
  } else if (finished(d.status)) {
    box.appendChild(el('p', { class: 'sub muted', text: d.status === 'failed' ? 'The call failed and the agent said nothing more.' : 'Nothing came back.' }))
  }
  if (d.truncated) box.appendChild(el('p', { class: 'sub muted', text: 'Some of this was cut at the host’s size limit.' }))
  return box
}

function stateText(d: ToolCallDetail): string {
  switch (d.status) {
    case 'completed':
      return 'done'
    case 'failed':
      return 'failed'
    case 'in_progress':
      return 'running…'
    case 'pending':
      return 'waiting to run'
    default:
      return d.status
  }
}

/** How long a call took, from its first report to its last. */
function took(from: string, to: string): string {
  const ms = Math.max(0, Date.parse(to) - Date.parse(from))
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  const m = Math.floor(ms / 60_000)
  return `${m} min ${Math.round((ms - m * 60_000) / 1000)} s`
}

/** The input, one field per line: a short string inline, anything longer or structured as a block. */
function inputFields(input: unknown): HTMLElement[] {
  if (input === undefined || input === null) return []
  const field = (key: string, v: unknown): HTMLElement => {
    const text = typeof v === 'string' ? v : JSON.stringify(v, null, 2)
    const short = typeof v !== 'object' && !text.includes('\n') && text.length <= 96
    return el('div', { class: 'field' }, [el('span', { class: 'key', text: key }), short ? el('code', { text }) : el('pre', { text })])
  }
  if (typeof input === 'object' && !Array.isArray(input)) {
    return Object.entries(input as Record<string, unknown>).map(([k, v]) => field(k, v))
  }
  return [field('input', input)]
}

/** What the call produced: each piece of content, or the raw output when nothing else carried the result. */
function outputBlocks(d: ToolCallDetail): HTMLElement[] {
  const out: HTMLElement[] = []
  for (const c of d.content ?? []) out.push(contentBlock(c))
  if (!out.length && d.output) out.push(el('pre', { class: 'out', text: d.output }))
  return out
}

function contentBlock(c: ToolCallContent): HTMLElement {
  switch (c.type) {
    case 'text':
      return el('pre', { class: 'out', text: unfenced(c.text ?? '') })
    case 'diff':
      return el('div', { class: 'diff' }, [
        c.path ? el('code', { class: 'path', text: c.path }) : null,
        c.oldText !== undefined && c.oldText !== null ? el('pre', { class: 'old', text: c.oldText }) : null,
        el('pre', { class: 'new', text: c.newText ?? '' }),
      ])
    case 'terminal':
      return el('p', { class: 'sub muted', text: `The output went to terminal ${c.terminalId ?? ''}.` })
    default:
      return el('p', { class: 'sub muted', text: c.text || c.type })
  }
}

/** Agents wrap a command's output in a code fence for a chat; the block here is already one, so the fence goes. */
function unfenced(text: string): string {
  const m = /^\s*(`{3,})[^\n]*\n([\s\S]*?)\n?\1\s*$/.exec(text)
  return m ? m[2] : text
}
