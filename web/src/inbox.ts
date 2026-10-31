import { el } from './dom'
import type { FetchCache } from './fetchcache'
import { renderMarkdown } from './markdown'
import { agentGlyph, timeNode } from './panels/parts'
import type { Session } from './protocol'
import { replyBox } from './reply'
import { agentLabel, isOpen, sessionLabel, sessionStatus } from './status'
import { conversationList, isUnread, isWaiting, type State } from './store'
import { parseTranscript } from './transcript'

export interface InboxActions {
  /** Opens one conversation, or none. */
  open(id: string | null): void
  /** Lists open conversations or all of them. */
  filter(which: 'open' | 'all'): void
  /** Shows the session in the world instead. */
  openInWorld(s: Session): void
  prompt(s: Session, text: string): Promise<string>
  stop(s: Session): Promise<string>
}

let lastDeps: unknown[] | null = null

/**
 * The inbox: every conversation on every island in one table, and the one
 * that is open beside a shorter list. It is rebuilt only when something it
 * shows has changed. Scroll positions survive a rebuild, and the
 * conversation stays pinned to its end while it was there.
 */
export function renderInbox(root: HTMLElement, s: State, cache: FetchCache, actions: InboxActions): void {
  const open = s.inbox ? s.sessions[s.inbox] : undefined
  const fetched = open ? cache.get('transcript', open.host, open.id, open.status === 'running' ? 2000 : 0, Date.parse(open.updatedAt)) : undefined
  const deps = [s.sessions, s.repos, s.viewed, s.inbox, s.inboxFilter, open, fetched?.at, fetched?.loading, fetched?.error, fetched?.text]
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  lastDeps = deps

  const scrolls = new Map<string, { top: number; atEnd: boolean }>()
  for (const box of root.querySelectorAll<HTMLElement>('[data-scroll]')) {
    scrolls.set(box.dataset.scroll as string, { top: box.scrollTop, atEnd: box.scrollHeight - box.scrollTop - box.clientHeight < 12 })
  }
  const openedBefore = root.querySelector<HTMLElement>('.convo')?.dataset.session

  root.textContent = ''
  root.appendChild(open ? narrowList(s, open.id, actions) : table(s, actions))
  if (open) root.appendChild(conversation(s, open, fetched?.text, fetched?.error, actions))

  for (const box of root.querySelectorAll<HTMLElement>('[data-scroll]')) {
    const key = box.dataset.scroll as string
    const was = scrolls.get(key)
    if (key === 'convo' && (!was || openedBefore !== open?.id || was.atEnd)) box.scrollTop = box.scrollHeight
    else if (was) box.scrollTop = was.top
  }
}

/** Copies one message's Markdown as the agent wrote it. */
function copyButton(text: string): HTMLElement {
  const b = el('button', { type: 'button', class: 'copy', text: 'Copy', 'aria-label': 'Copy this message' })
  b.addEventListener('click', () => {
    navigator.clipboard?.writeText(text).then(
      () => {
        b.textContent = 'Copied'
        setTimeout(() => (b.textContent = 'Copy'), 1500)
      },
      () => (b.textContent = 'Copy failed'),
    )
  })
  return b
}

/** A session's mark: filled while it is open, a ring once it is over. */
function mark(s: State, x: Session): HTMLElement {
  const status = sessionStatus(x, isWaiting(s, x))
  const style = isOpen(x) ? `background:${status[1]}` : `background:transparent;border:2px solid ${status[1]}`
  return el('span', { class: 'mark', title: status[0], style })
}

/** The conversations the inbox lists under the current filter. An open conversation stays listed while it is open. */
function listed(s: State): Session[] {
  const all = conversationList(s)
  return s.inboxFilter === 'all' ? all : all.filter((x) => isOpen(x) || x.id === s.inbox)
}

/** The row above the list: what is shown, and the switch between open and all. */
function tools(s: State, actions: InboxActions, compact: boolean): HTMLElement {
  const all = conversationList(s)
  const open = all.filter(isOpen).length
  const over = all.length - open
  const seg = (which: 'open' | 'all', text: string): HTMLElement =>
    el('button', { type: 'button', class: 'btn small', 'aria-pressed': s.inboxFilter === which ? 'true' : 'false', text, onclick: () => actions.filter(which) })
  const count = compact ? null : el('span', { class: 'muted', text: s.inboxFilter === 'all' ? `${all.length} conversations` : `${open} open${over ? `, ${over} finished hidden` : ''}` })
  return el('div', { class: 'inbox-tools' }, [count, el('div', { class: 'modes', role: 'group', 'aria-label': 'Which conversations' }, [seg('open', 'Open'), seg('all', 'All')])])
}

function agentCell(x: Session): HTMLElement {
  return el('span', { class: 'who' }, [agentGlyph(x.agent), agentLabel(x.agent)])
}

/** The full-width inbox: one line per conversation. */
function table(s: State, actions: InboxActions): HTMLElement {
  const list = listed(s)
  const box = el('div', { class: 'inbox-list', 'data-scroll': 'list' })
  box.appendChild(tools(s, actions, false))
  if (!list.length) {
    const any = Object.keys(s.sessions).length > 0
    box.appendChild(el('p', { class: 'inbox-empty', text: any ? 'No open conversations. Switch to All to see finished ones.' : 'No conversations yet. Open a terrace in the world and give an agent some work.' }))
    return box
  }
  const head = el('tr', {}, ['Conversation', 'Agent', 'Repository', 'Status', 'Turns', 'Last activity'].map((h) => el('th', { scope: 'col', text: h })))
  const body = el('tbody')
  for (const x of list) {
    const status = sessionStatus(x, isWaiting(s, x))
    const unread = isUnread(s, x)
    const row = el('tr', { tabindex: '0', class: unread ? 'unread' : '', 'aria-label': `${sessionLabel(x)}, ${agentLabel(x.agent)} on ${s.repos[x.repoId]?.name ?? x.repoId}` }, [
      el('td', { title: x.prompt }, [el('span', { class: 'who' }, [unread ? el('span', { class: 'dot new', title: 'New since you last looked' }) : null, sessionLabel(x)])]),
      el('td', {}, [agentCell(x)]),
      el('td', { text: s.repos[x.repoId]?.name ?? x.repoId }),
      el('td', {}, [el('span', { class: 'who' }, [mark(s, x), status[0]])]),
      el('td', { text: String(x.turns) }),
      el('td', {}, [timeNode(x.updatedAt, 'ago')]),
    ])
    row.addEventListener('click', () => actions.open(x.id))
    row.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        actions.open(x.id)
      }
    })
    body.appendChild(row)
  }
  const t = el('table', { class: 'inbox-table' }, [
    el('colgroup', {}, [38, 14, 14, 15, 6, 13].map((w) => el('col', { style: `width:${w}%` }))),
    el('thead', {}, [head]),
    body,
  ])
  box.appendChild(t)
  return box
}

/** The list beside an open conversation: one line each. */
function narrowList(s: State, openId: string, actions: InboxActions): HTMLElement {
  const box = el('div', { class: 'inbox-list narrow', 'data-scroll': 'list' })
  box.appendChild(tools(s, actions, true))
  for (const x of listed(s)) {
    const unread = isUnread(s, x) && x.id !== openId
    const row = el(
      'button',
      { type: 'button', class: `conv-row${unread ? ' unread' : ''}`, 'aria-current': x.id === openId ? 'true' : 'false', title: x.prompt, onclick: () => actions.open(x.id) },
      [
        mark(s, x),
        el('span', { class: 'name', text: sessionLabel(x, 48) }),
        el('span', { class: 'repo', text: `${agentLabel(x.agent)} \u00b7 ${s.repos[x.repoId]?.name ?? x.repoId}` }),
        unread ? el('span', { class: 'dot new' }) : null,
        el('span', { class: 'when' }, [timeNode(x.updatedAt, 'ago')]),
      ],
    )
    box.appendChild(row)
  }
  return box
}

/** One conversation: its turns, how it ended, and the reply box while it is open. */
function conversation(s: State, x: Session, transcript: string | undefined, fetchError: string | undefined, actions: InboxActions): HTMLElement {
  const status = sessionStatus(x, isWaiting(s, x))
  const repo = s.repos[x.repoId]
  const name = agentLabel(x.agent)
  const head = el('div', { class: 'convo-head' }, [
    el('h2', { text: sessionLabel(x), title: x.prompt }),
    el('span', { class: 'who muted' }, [agentGlyph(x.agent), `${name} \u00b7 ${repo?.name ?? x.repoId} on ${x.host}`]),
    el('span', { class: 'pill' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]),
    el('span', { class: 'muted', text: `${x.turns} ${x.turns === 1 ? 'turn' : 'turns'}` }),
    el('div', { class: 'acts' }, [
      el('button', { type: 'button', class: 'btn small ghost', text: 'Show in world', onclick: () => actions.openInWorld(x) }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Close', 'aria-label': 'Close the conversation', onclick: () => actions.open(null) }),
    ]),
  ])

  const body = el('div', { class: 'convo-body', 'data-scroll': 'convo' })
  const conv = parseTranscript(transcript ?? '')
  if (!conv.turns.length) conv.turns.push({ who: 'you', text: x.prompt })
  for (const t of conv.turns) {
    if (t.who === 'you') body.appendChild(el('div', { class: 'msg you' }, [el('span', { class: 'from', text: 'You' }), el('div', { class: 'body', text: t.text })]))
    else if (t.text) body.appendChild(el('div', { class: 'msg agent' }, [el('span', { class: 'from' }, [name, copyButton(t.text)]), el('div', { class: 'body md' }, [renderMarkdown(t.text)])]))
  }
  if (x.status === 'running') {
    const doing = x.currentAction ?? (x.agentName ? 'Thinking' : 'Starting')
    body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), el('div', { class: 'body', text: `${doing}\u2026` })]))
  }
  if (fetchError) body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: fetchError }))
  if (x.status === 'failed') body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: `Failed: ${x.error ?? 'unknown error'}` }))
  else if (x.status === 'ended') body.appendChild(el('p', { class: 'convo-end', text: x.stopReason === 'stopped' ? 'You ended this session.' : `This session ended (${(x.stopReason ?? 'done').replace(/_/g, ' ')}).` }))

  const foot = el('div', { class: 'convo-foot' })
  if (isOpen(x)) foot.appendChild(replyBox(x, { prompt: (text) => actions.prompt(x, text), stop: () => actions.stop(x) }))
  else foot.appendChild(el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'This session is over. Start a new one from its terrace in the world.' }))

  const convo = el('section', { class: 'convo', 'aria-label': `${sessionLabel(x)}, with ${name}`, 'data-session': x.id }, [head, body, foot])
  return convo
}
