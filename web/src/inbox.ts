import { el } from './dom'
import type { FetchCache, Fetched } from './fetchcache'
import type { LiveText } from './live'
import { renderMarkdown } from './markdown'
import { agentGlyph, timeNode } from './panels/parts'
import type { Permission, Session } from './protocol'
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
  /** Answers a waiting tool call. Resolves with an error message, or empty. */
  approve(s: Session, p: Permission, optionId: string): Promise<string>
  deny(s: Session, p: Permission): Promise<string>
  /** Allows every tool call in the session without asking, or asks again. */
  trust(s: Session, trusted: boolean): Promise<string>
  /** Commits everything in the worktree. Resolves with an error message, or empty. */
  commit(s: Session, message: string): Promise<string>
  /** Merges the session's branch into its base in the checkout. */
  merge(s: Session): Promise<string>
  /** Pushes the branch and opens a pull request from it. */
  pullRequest(s: Session): Promise<string>
}

let lastDeps: unknown[] | null = null

/**
 * The inbox: every conversation on every island in one table, and the one
 * that is open beside a shorter list. It is rebuilt only when something it
 * shows has changed. Scroll positions survive a rebuild, and the
 * conversation stays pinned to its end while it was there.
 */
export function renderInbox(root: HTMLElement, s: State, cache: FetchCache, live: LiveText, actions: InboxActions): void {
  const open = s.inbox ? s.sessions[s.inbox] : undefined
  // While a turn runs the live feed carries the transcript, with a slow
  // re-read as a safety net. Once the turn is over it is read again, so
  // what stays on screen is the file and not the feed.
  const fetched = open
    ? open.status === 'running'
      ? cache.get('transcript', open.host, open.id, 15000)
      : cache.get('transcript', open.host, open.id, 0, Date.parse(open.updatedAt))
    : undefined
  // Once the agent is not mid-turn, where its changes stand is read from the
  // host, and read again after each event, such as a commit.
  const changes = open && open.status !== 'running' && !open.worktreeRemoved ? cache.get('changes', open.host, open.id, 0, Date.parse(open.updatedAt)) : undefined
  const deps = [s.sessions, s.repos, s.viewed, s.inbox, s.inboxFilter, open, fetched?.at, fetched?.loading, fetched?.error, fetched?.text, changes?.at, changes?.loading, changes?.error, changes?.changes]
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  lastDeps = deps

  const scrolls = new Map<string, { top: number; atEnd: boolean }>()
  for (const box of root.querySelectorAll<HTMLElement>('[data-scroll]')) {
    scrolls.set(box.dataset.scroll as string, { top: box.scrollTop, atEnd: box.scrollHeight - box.scrollTop - box.clientHeight < 12 })
  }
  const openedBefore = root.querySelector<HTMLElement>('.convo')?.dataset.session

  root.textContent = ''
  root.appendChild(open ? narrowList(s, open.id, actions) : table(s, actions))
  if (open) {
    const { text, gap } = live.compose(open.id, fetched)
    if (gap && fetched && !fetched.loading) cache.get('transcript', open.host, open.id, 0, Date.now())
    root.appendChild(conversation(s, open, text, fetched?.error, changes, actions))
  }

  for (const box of root.querySelectorAll<HTMLElement>('[data-scroll]')) {
    const key = box.dataset.scroll as string
    const was = scrolls.get(key)
    if (key === 'convo' && (!was || openedBefore !== open?.id || was.atEnd)) box.scrollTop = box.scrollHeight
    else if (was) box.scrollTop = was.top
  }
}

/**
 * A tool call waiting on you: what the agent wants to do, the exact input
 * it proposed, and the answers it accepts.
 */
function approvalCard(x: Session, p: Permission, actions: InboxActions): HTMLElement {
  const note = el('span', { class: 'sub' })
  const buttons: HTMLButtonElement[] = []
  const decide = async (run: () => Promise<string>, saying: string): Promise<void> => {
    for (const b of buttons) b.disabled = true
    note.textContent = saying
    const err = await run()
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      for (const b of buttons) b.disabled = false
    }
  }
  const allows = p.options.filter((o) => o.kind.startsWith('allow'))
  const rejects = p.options.filter((o) => o.kind.startsWith('reject'))
  for (const o of allows) {
    const b = el('button', { type: 'button', class: `btn small ${o.kind === 'allow_once' ? 'primary' : ''}`, text: o.name, onclick: () => void decide(() => actions.approve(x, p, o.id), 'Allowing\u2026') }) as HTMLButtonElement
    buttons.push(b)
  }
  const d = el('button', { type: 'button', class: 'btn small ghost', text: rejects[0]?.name ?? 'Deny', onclick: () => void decide(() => actions.deny(x, p), 'Denying\u2026') }) as HTMLButtonElement
  buttons.push(d)
  const where = p.locations?.length ? el('div', { class: 'where' }, [el('code', { text: p.locations.join(', ') })]) : null
  let input: HTMLElement | null = null
  if (p.input !== undefined && p.input !== null) {
    const text = typeof p.input === 'string' ? p.input : JSON.stringify(p.input, null, 2)
    input = el('details', {}, [el('summary', { text: 'Exact input' }), el('pre', { text })])
  }
  return el('div', { class: 'approval', role: 'group', 'aria-label': `Approval needed: ${p.title}` }, [
    el('div', { class: 'ask' }, [el('span', { class: 'mark', style: 'background:#E0B000' }), el('strong', { text: p.title }), p.kind ? el('span', { class: 'kindchip', text: p.kind }) : null, el('span', { class: 'muted', style: 'margin-left:auto;font-size:12px' }, ['asked ', timeNode(p.at, 'ago')])]),
    where,
    input,
    el('div', { class: 'acts' }, [...buttons, note]),
  ])
}

/** Unsent commit messages by session, so a rebuild keeps what was typed. */
const messages = new Map<string, string>()

/**
 * Where the session's changes stand, and what to do with them: commit
 * what is uncommitted, and for a session on its own branch, merge it into
 * its base in the checkout or open a pull request from it. A session in
 * the checkout commits on the checkout's own branch and that is all.
 */
function changesCard(x: Session, f: Fetched | undefined, actions: InboxActions): HTMLElement {
  const box = el('div', { class: 'changes', role: 'group', 'aria-label': 'Changes' })
  const where = x.worktreeRemoved ? 'The worktree was removed.' : x.inCheckout ? `In the checkout, on ${x.branch}` : `On ${x.branch}${x.base ? `, from ${x.base}` : ''}`
  box.appendChild(el('div', { class: 'ask' }, [el('strong', { text: 'Changes' }), el('span', { class: 'muted', text: where })]))
  const done: (Node | string)[] = []
  if (x.mergedInto) done.push(`Merged into ${x.mergedInto} as ${x.mergedCommit?.slice(0, 7) ?? ''}.`)
  if (x.pullRequest) done.push(done.length ? ' ' : '', 'Pull request ', el('a', { href: x.pullRequest, target: '_blank', rel: 'noopener', text: x.pullRequest.replace(/^https?:\/\//, '') }), '.')
  if (x.worktreeRemoved) {
    if (done.length) box.appendChild(el('span', { class: 'sub' }, done))
    return box
  }
  if (!f || (f.loading && f.at === 0)) {
    box.appendChild(el('span', { class: 'sub', text: 'Looking at the worktree\u2026' }))
    return box
  }
  if (f.error || !f.changes) {
    box.appendChild(el('span', { class: 'sub', style: 'color:var(--bad)', text: f.error ?? 'No answer from the host.' }))
    return box
  }
  const c = f.changes
  const note = el('span', { class: 'sub' })
  const buttons: HTMLButtonElement[] = []
  const act = async (run: () => Promise<string>, saying: string): Promise<void> => {
    for (const b of buttons) b.disabled = true
    note.textContent = saying
    note.style.color = ''
    const err = await run()
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      for (const b of buttons) b.disabled = false
    }
  }
  const button = (text: string, primary: boolean, onclick: () => void): HTMLButtonElement => {
    const b = el('button', { type: 'button', class: `btn small ${primary ? 'primary' : 'ghost'}`, text, onclick }) as HTMLButtonElement
    buttons.push(b)
    return b
  }
  const dirty = c.changed.length > 0
  const canMerge = !x.inCheckout && (dirty || c.ahead > 0)
  if (dirty) {
    const n = c.changed.length
    const shown = c.changed.slice(0, 6).join(', ') + (n > 6 ? ', \u2026' : '')
    box.appendChild(el('div', { class: 'sub' }, [`${n} ${n === 1 ? 'file' : 'files'} changed since the last commit: `, el('code', { text: shown })]))
    const input = el('input', { type: 'text', 'aria-label': 'Commit message', placeholder: 'Commit message' }) as HTMLInputElement
    input.value = messages.get(x.id) ?? sessionLabel(x, 72)
    input.addEventListener('input', () => messages.set(x.id, input.value))
    const commit = (): Promise<string> => actions.commit(x, input.value.trim())
    const row = el('div', { class: 'acts' }, [input, button('Commit', !canMerge, () => void act(commit, 'Committing\u2026'))])
    if (canMerge && x.base) {
      row.appendChild(button(`Commit and merge into ${x.base}`, true, () => void act(async () => (await commit()) || actions.merge(x), 'Committing and merging\u2026')))
      if (!x.pullRequest) row.appendChild(button('Commit and open a pull request', false, () => void act(async () => (await commit()) || actions.pullRequest(x), 'Committing and pushing\u2026')))
    }
    box.appendChild(row)
  } else if (canMerge) {
    const row = el('div', { class: 'acts' }, [el('span', { class: 'sub', text: `${c.ahead} ${c.ahead === 1 ? 'commit' : 'commits'} ahead of ${c.base}.` })])
    row.appendChild(button(`Merge into ${c.base}`, true, () => void act(() => actions.merge(x), 'Merging\u2026')))
    if (!x.pullRequest) row.appendChild(button('Open a pull request', false, () => void act(() => actions.pullRequest(x), 'Pushing\u2026')))
    box.appendChild(row)
  } else {
    box.appendChild(el('span', { class: 'sub', text: x.commits ? 'Everything is committed.' : 'Nothing to commit.' }))
  }
  if (done.length) box.appendChild(el('span', { class: 'sub' }, done))
  box.appendChild(note)
  return box
}

/** The switch that lets a session's tool calls through without asking. */
function trustSwitch(x: Session, actions: InboxActions): HTMLElement {
  const input = el('input', { type: 'checkbox', 'aria-label': 'Trust this session' }) as HTMLInputElement
  input.checked = !!x.trusted
  const label = el('label', { class: 'trust', title: 'Allow every tool call in this session without asking' }, [input, 'Trust this session'])
  input.addEventListener('change', async () => {
    input.disabled = true
    const err = await actions.trust(x, input.checked)
    input.disabled = false
    if (err) {
      input.checked = !input.checked
      label.title = err
    }
  })
  return label
}

/** An agent message: its words as Markdown, with each tool call it made as a chip where it happened. */
function agentBody(text: string): HTMLElement {
  const body = el('div', { class: 'body md' })
  for (const seg of segments(text)) {
    if (seg.kind === 'tool') body.appendChild(el('span', { class: 'toolchip', text: seg.text }))
    else body.appendChild(renderMarkdown(seg.text))
  }
  return body
}

/** The running turn's body: what the agent has said so far, or what it is doing. */
function liveBody(x: Session, text: string): HTMLElement {
  if (text.trim()) {
    const body = agentBody(text)
    body.classList.add('md')
    return body
  }
  const doing = x.currentAction ?? (x.agentName ? 'Thinking' : 'Starting')
  return el('div', { class: 'body quiet', text: `${doing}\u2026` })
}

/** A line the daemon writes for a tool call, between the agent's words. */
const TOOL_LINE = /^\u2699 (.*)$/

/** Splits an agent turn into runs of words and the tool calls between them. */
function segments(text: string): { kind: 'text' | 'tool'; text: string }[] {
  const out: { kind: 'text' | 'tool'; text: string }[] = []
  let words: string[] = []
  const flush = (): void => {
    const t = words.join('\n').trim()
    if (t) out.push({ kind: 'text', text: t })
    words = []
  }
  for (const line of text.split('\n')) {
    const m = TOOL_LINE.exec(line)
    if (m) {
      flush()
      out.push({ kind: 'tool', text: m[1] })
    } else words.push(line)
  }
  flush()
  return out
}

/**
 * Grows the open conversation's live bubble with what the feed brought,
 * without rebuilding anything else. Called as pieces arrive.
 */
export function growLive(root: HTMLElement, s: State, cache: FetchCache, live: LiveText): void {
  const open = s.inbox ? s.sessions[s.inbox] : undefined
  const convo = root.querySelector<HTMLElement>('.convo')
  if (!open || !convo || convo.dataset.session !== open.id || open.status !== 'running') return
  const fetched = cache.peek('transcript', open.host, open.id)
  const { text, gap } = live.compose(open.id, fetched)
  if (gap && fetched && !fetched.loading) cache.get('transcript', open.host, open.id, 0, Date.now())
  const conv = parseTranscript(text)
  const last = conv.turns[conv.turns.length - 1]
  const bubble = convo.querySelector<HTMLElement>('.msg.live')
  if (!bubble || !last || last.who !== 'agent') return
  const box = convo.querySelector<HTMLElement>('.convo-body')
  const atEnd = !!box && box.scrollHeight - box.scrollTop - box.clientHeight < 12
  bubble.querySelector('.body')?.replaceWith(liveBody(open, last.text))
  if (box && atEnd) box.scrollTop = box.scrollHeight
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

/** One conversation: its turns, the notes between them, how it ended, and the reply box while it is open. */
function conversation(s: State, x: Session, transcript: string | undefined, fetchError: string | undefined, changes: Fetched | undefined, actions: InboxActions): HTMLElement {
  const status = sessionStatus(x, isWaiting(s, x))
  const repo = s.repos[x.repoId]
  const name = agentLabel(x.agent)
  const head = el('div', { class: 'convo-head' }, [
    el('h2', { text: sessionLabel(x), title: x.prompt }),
    el('span', { class: 'who muted' }, [agentGlyph(x.agent), `${name} \u00b7 ${repo?.name ?? x.repoId} on ${x.host}`]),
    el('span', { class: 'pill' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]),
    el('span', { class: 'muted', text: `${x.turns} ${x.turns === 1 ? 'turn' : 'turns'}` }),
    el('div', { class: 'acts' }, [
      isOpen(x) ? trustSwitch(x, actions) : null,
      el('button', { type: 'button', class: 'btn small ghost', text: 'Show in world', onclick: () => actions.openInWorld(x) }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Close', 'aria-label': 'Close the conversation', onclick: () => actions.open(null) }),
    ]),
  ])

  const body = el('div', { class: 'convo-body', 'data-scroll': 'convo' })
  const conv = parseTranscript(transcript ?? '')
  if (!conv.turns.length) conv.turns.push({ who: 'you', text: x.prompt })
  const running = x.status === 'running'
  // While a turn runs, its last agent turn is the live bubble, which grows
  // in place as the feed arrives rather than rebuilding the conversation.
  const last = conv.turns[conv.turns.length - 1]
  const liveTurn = running && last?.who === 'agent' ? last : null
  for (const t of conv.turns) {
    if (t.who === 'you') body.appendChild(el('div', { class: 'msg you' }, [el('span', { class: 'from', text: 'You' }), el('div', { class: 'body', text: t.text })]))
    else if (t.who === 'note') body.appendChild(el('p', { class: 'convo-note', text: t.text }))
    else if (t === liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, t.text)]))
    else if (t.text) body.appendChild(el('div', { class: 'msg agent' }, [el('span', { class: 'from' }, [name, copyButton(t.text)]), agentBody(t.text)]))
  }
  if (running && !liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, '')]))
  for (const p of x.pending) body.appendChild(approvalCard(x, p, actions))
  if (fetchError) body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: fetchError }))
  if (x.status === 'failed') body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: `Failed: ${x.error ?? 'unknown error'}` }))
  else if (x.status === 'ended') body.appendChild(el('p', { class: 'convo-end', text: x.stopReason === 'stopped' ? 'You ended this session.' : `This session ended (${(x.stopReason ?? 'done').replace(/_/g, ' ')}).` }))

  const foot = el('div', { class: 'convo-foot' })
  if (x.status !== 'running') foot.appendChild(changesCard(x, changes, actions))
  if (isOpen(x)) foot.appendChild(replyBox(x, { prompt: (text) => actions.prompt(x, text), stop: () => actions.stop(x) }))
  else foot.appendChild(el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'This session is over. Start a new one from its terrace in the world.' }))

  const hint = isOpen(x) && x.trusted ? el('p', { class: 'trust-hint', text: 'Tool calls in this session run without asking.' }) : null
  const convo = el('section', { class: 'convo', 'aria-label': `${sessionLabel(x)}, with ${name}`, 'data-session': x.id }, [head, hint, body, foot])
  return convo
}
