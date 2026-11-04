import { el } from './dom'
import type { FetchCache, Fetched } from './fetchcache'
import type { LiveText } from './live'
import { renderMarkdown } from './markdown'
import { agentGlyph, timeNode } from './panels/parts'
import type { Permission, Question, QuestionField, Session } from './protocol'
import { replyBox } from './reply'
import { inputsVersion, requestInput } from './requestinput'
import { HOST_DEFAULTS, proposeRule, ruleText, rulesSheet } from './rules'
import { agentLabel, isOpen, sessionLabel, sessionStatus, tokensBreakdown, tokensLabel, tokensText } from './status'
import { conversationList, isUnread, isWaiting, sumTokens, type State } from './store'
import { chipDeps, toolChip } from './toolchip'

import { parseTranscript } from './transcript'
import type { Rule } from './protocol'

export interface InboxActions {
  /** Opens one conversation, or none. */
  open(id: string | null): void
  /** Lists open conversations or all of them. */
  filter(which: 'open' | 'all'): void
  /** Shows the session in the world instead. */
  openInWorld(s: Session): void
  prompt(s: Session, text: string): Promise<string>
  /** Interrupts the turn in progress and keeps the session. */
  cancel(s: Session): Promise<string>
  stop(s: Session): Promise<string>
  /** Draws the inbox again, as when a request's whole input arrives. */
  redraw(): void
  /** Answers a waiting tool call, naming the hash of the input the person saw. Resolves with an error message, or empty. */
  approve(s: Session, p: Permission, optionId: string, inputHash: string): Promise<string>
  deny(s: Session, p: Permission): Promise<string>
  /** Answers a question the agent asked, with values by field key, or declines it. */
  answer(s: Session, q: Question, answers: Record<string, unknown>): Promise<string>
  decline(s: Session, q: Question): Promise<string>
  /** Allows every tool call in the session without asking, or asks again. */
  trust(s: Session, trusted: boolean): Promise<string>
  /** Commits everything in the worktree. Resolves with an error message, or empty. */
  commit(s: Session, message: string): Promise<string>
  /** Merges the session's branch into its base in the checkout. */
  merge(s: Session): Promise<string>
  /** Pushes the branch and opens a pull request from it. */
  pullRequest(s: Session): Promise<string>
  /** Replaces a session's rules, or its repository's, which new sessions copy. */
  setSessionRules(s: Session, rules: Rule[]): Promise<string>
  setRepoRules(host: string, repoId: string, rules: Rule[]): Promise<string>
  /** Re-renders, for a change that lives only in this view. */
  refresh(): void
}

/** The sessions whose rules sheet is open, and a counter so a rebuild notices a toggle. */
const rulesOpen = new Set<string>()
let rulesToggles = 0

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
  const deps = [s.sessions, s.repos, s.viewed, s.inbox, s.inboxFilter, open, inputsVersion(), fetched?.at, fetched?.loading, fetched?.error, fetched?.text, changes?.at, changes?.loading, changes?.error, changes?.changes, rulesToggles, ...(open ? chipDeps(open, cache) : [])]
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
    root.appendChild(conversation(s, open, text, fetched?.error, changes, cache, actions))
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
 * it proposed, and the answers it accepts. Besides answering once, you can
 * keep the answer as a rule for this session or for every session on the
 * repository, with the pattern the request suggests, edited as you like.
 */
function approvalCard(s: State, x: Session, p: Permission, actions: InboxActions): HTMLElement {
  // The whole input, which the person sees and approves by its hash. Until
  // it is here and its hash checks out, nothing can be allowed.
  const view = requestInput(x.host, x.id, p, actions.redraw)
  const note = el('span', { class: 'sub' })
  const buttons: HTMLButtonElement[] = []
  const enable = (): void => {
    for (const b of buttons) b.disabled = b.dataset.allows === 'true' && !view.hash
  }
  const decide = async (run: () => Promise<string>, saying: string): Promise<void> => {
    for (const b of buttons) b.disabled = true
    note.textContent = saying
    note.style.color = ''
    const err = await run()
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      enable()
    }
  }
  if (view.loading) note.textContent = `Getting the whole input from ${x.host} (${sizeText(p.inputSize)})\u2026`
  if (view.error) {
    note.textContent = view.error
    note.style.color = 'var(--bad)'
  }
  const allows = p.options.filter((o) => o.kind.startsWith('allow'))
  const rejects = p.options.filter((o) => o.kind.startsWith('reject'))
  const once = allows.find((o) => o.kind === 'allow_once') ?? allows[0]
  if (once) {
    buttons.push(el('button', { type: 'button', class: 'btn small primary', 'data-allows': 'true', text: once.kind === 'allow_once' ? 'Allow once' : once.name, onclick: () => void decide(() => actions.approve(x, p, once.id, view.hash ?? ''), 'Allowing\u2026') }) as HTMLButtonElement)
  }
  buttons.push(el('button', { type: 'button', class: 'btn small ghost', text: rejects[0]?.name ?? 'Deny', onclick: () => void decide(() => actions.deny(x, p), 'Denying\u2026') }) as HTMLButtonElement)

  // Remember the answer as a rule. The request's answer follows once the
  // rule is saved, so a refused rule leaves the request waiting.
  const repo = s.repos[x.repoId]
  const proposed = proposeRule(p, view.parsed, 'allow')
  const decision = el('select', { 'aria-label': 'Decision to remember' }, [el('option', { value: 'allow', text: 'allow' }), el('option', { value: 'deny', text: 'deny' })]) as HTMLSelectElement
  const pattern = el('input', { type: 'text', 'aria-label': 'Pattern', placeholder: 'any' }) as HTMLInputElement
  pattern.value = proposed.pattern ?? ''
  const rule = (): Rule => ({ decision: decision.value as Rule['decision'], kind: proposed.kind, pattern: pattern.value.trim() || undefined })
  const answer = (): Promise<string> => (decision.value === 'allow' && once ? actions.approve(x, p, once.id, view.hash ?? '') : actions.deny(x, p))
  const forSession = el('button', { type: 'button', class: 'btn small', text: 'For this session', onclick: () => void decide(async () => (await actions.setSessionRules(x, [...x.rules, rule()])) || answer(), 'Saving the rule\u2026') }) as HTMLButtonElement
  const forRepo = el('button', {
    type: 'button',
    class: 'btn small',
    text: `For every session on ${repo?.name ?? 'this repository'}`,
    onclick: () =>
      void decide(async () => {
        const r = rule()
        const err = (await actions.setRepoRules(x.host, x.repoId, [...(repo?.rules ?? []), r])) || (await actions.setSessionRules(x, [...x.rules, r]))
        return err || answer()
      }, 'Saving the rule\u2026'),
  }) as HTMLButtonElement
  buttons.push(forSession, forRepo)
  const preview = el('span', { class: 'sub' })
  const refresh = (): void => {
    preview.textContent = `Rule: ${ruleText(rule())}`
  }
  decision.addEventListener('change', refresh)
  pattern.addEventListener('input', refresh)
  refresh()
  const remember = el('details', { class: 'remember' }, [
    el('summary', { text: 'Remember as a rule' }),
    el('div', { class: 'body' }, [el('div', { class: 'acts' }, [decision, el('span', { class: 'kind', text: proposed.kind }), pattern]), preview, el('div', { class: 'acts' }, [forSession, forRepo])]),
  ])

  const where = p.locations?.length ? el('div', { class: 'where' }, [el('code', { text: p.locations.join(', ') })]) : null
  let input: HTMLElement | null = null
  if (p.inputSize > 0) {
    const text =
      view.text === null
        ? `${view.preview}\u2026`
        : view.parsed !== null && typeof view.parsed === 'object'
          ? JSON.stringify(view.parsed, null, 2)
          : view.text
    input = el('details', {}, [el('summary', { text: view.text === null ? `Exact input (${sizeText(p.inputSize)}, the start)` : 'Exact input' }), el('pre', { text })])
  }
  enable()
  return el('div', { class: 'approval', role: 'group', 'aria-label': `Approval needed: ${p.title}` }, [
    el('div', { class: 'ask' }, [el('span', { class: 'mark', style: 'background:#E0B000' }), el('strong', { text: p.title }), p.kind ? el('span', { class: 'kindchip', text: p.kind }) : null, el('span', { class: 'muted', style: 'margin-left:auto;font-size:12px' }, ['asked ', timeNode(p.at, 'ago')])]),
    where,
    input,
    el('div', { class: 'acts' }, [buttons[0], buttons[1], remember, note]),
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

/** One field of a question, as a control: choices as radio buttons or checkboxes, otherwise an input. */
function questionControl(q: Question, f: QuestionField, read: Map<string, () => unknown>): HTMLElement {
  const name = `${q.questionId}-${f.key}`
  const label = el('div', { class: 'qlabel' }, [el('strong', { text: f.title ?? f.key }), f.description ? el('span', { class: 'muted', text: ` ${f.description}` }) : null])
  if (f.options?.length) {
    const multi = f.type === 'array'
    const inputs: HTMLInputElement[] = []
    const choices = f.options.map((o) => {
      const input = el('input', { type: multi ? 'checkbox' : 'radio', name, value: o.value }) as HTMLInputElement
      inputs.push(input)
      return el('label', { class: 'choice', title: o.description ?? '' }, [input, el('span', { text: o.title ?? o.value }), o.description ? el('span', { class: 'muted', text: o.description }) : null])
    })
    read.set(f.key, () => {
      const picked = inputs.filter((i) => i.checked).map((i) => i.value)
      return multi ? picked : picked[0]
    })
    return el('div', { class: 'qfield' }, [label, el('div', { class: 'choices' }, choices)])
  }
  if (f.type === 'boolean') {
    const input = el('input', { type: 'checkbox', name }) as HTMLInputElement
    read.set(f.key, () => input.checked)
    return el('div', { class: 'qfield' }, [el('label', { class: 'choice' }, [input, label])])
  }
  const input = el('input', { type: f.type === 'number' || f.type === 'integer' ? 'number' : 'text', name, 'aria-label': f.title ?? f.key, placeholder: f.description ?? '' }) as HTMLInputElement
  read.set(f.key, () => {
    const v = input.value.trim()
    if (v === '') return undefined
    return f.type === 'number' || f.type === 'integer' ? Number(v) : v
  })
  return el('div', { class: 'qfield' }, [label, input])
}

/** A question the agent is waiting on: its message, a control per field, and the answer or the refusal. */
function questionCard(x: Session, q: Question, actions: InboxActions): HTMLElement {
  const note = el('span', { class: 'sub' })
  const read = new Map<string, () => unknown>()
  const fields = q.fields.map((f) => questionControl(q, f, read))
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
  const answer = el('button', {
    type: 'button',
    class: 'btn small primary',
    text: 'Answer',
    onclick: () => {
      const answers: Record<string, unknown> = {}
      for (const [key, get] of read) {
        const v = get()
        if (v !== undefined && v !== '' && !(Array.isArray(v) && v.length === 0)) answers[key] = v
      }
      const missing = q.fields.filter((f) => f.required && answers[f.key] === undefined)
      if (missing.length) {
        note.textContent = `${missing[0].title ?? missing[0].key} needs an answer.`
        note.style.color = 'var(--bad)'
        return
      }
      void act(() => actions.answer(x, q, answers), 'Answering\u2026')
    },
  }) as HTMLButtonElement
  const decline = el('button', { type: 'button', class: 'btn small ghost', text: 'Decline to answer', onclick: () => void act(() => actions.decline(x, q), 'Declining\u2026') }) as HTMLButtonElement
  buttons.push(answer, decline)
  return el('div', { class: 'approval question', role: 'group', 'aria-label': `The agent asks: ${q.message}` }, [
    el('div', { class: 'ask' }, [el('span', { class: 'mark', style: 'background:#E0B000' }), el('strong', { text: q.message }), el('span', { class: 'muted', style: 'margin-left:auto;font-size:12px' }, ['asked ', timeNode(q.at, 'ago')])]),
    ...fields,
    el('div', { class: 'acts' }, [answer, decline, note]),
  ])
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

/** An agent message: its words as Markdown, with each tool call it made as a chip where it happened, and its record behind the chip. */
function agentBody(x: Session, text: string, cache: FetchCache, refresh: () => void): HTMLElement {
  const body = el('div', { class: 'body md' })
  for (const seg of segments(text)) {
    if (seg.kind === 'tool') for (const n of toolChip(x, seg.text, seg.id, cache, refresh)) body.appendChild(n)
    else body.appendChild(renderMarkdown(seg.text))
  }
  return body
}

/** The running turn's body: what the agent has said so far, or what it is doing. */
function liveBody(x: Session, text: string, cache: FetchCache, refresh: () => void): HTMLElement {
  if (text.trim()) {
    const body = agentBody(x, text, cache, refresh)
    body.classList.add('md')
    return body
  }
  const doing = x.currentAction ?? (x.agentName ? 'Thinking' : 'Starting')
  return el('div', { class: 'body quiet', text: `${doing}\u2026` })
}

/**
 * A line the daemon writes between the agent's words: a tool call, or a
 * question and its answer. A tool call's line ends with a marker naming
 * the call, which Markdown would hide and the chip follows to its record.
 */
const TOOL_LINE = /^[\u2699\u2753] (.*?)(?: <!-- tool:(\S+) -->)?\s*$/

/** The marker, wherever it is, for text that leaves the page. */
const TOOL_MARKERS = / <!-- tool:\S+ -->/g

/** Splits an agent turn into runs of words and the tool calls between them. */
function segments(text: string): { kind: 'text' | 'tool'; text: string; id?: string }[] {
  const out: { kind: 'text' | 'tool'; text: string; id?: string }[] = []
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
      out.push({ kind: 'tool', text: m[1], id: m[2] })
    } else words.push(line)
  }
  flush()
  return out
}

/**
 * Grows the open conversation's live bubble with what the feed brought,
 * without rebuilding anything else. Called as pieces arrive.
 */
export function growLive(root: HTMLElement, s: State, cache: FetchCache, live: LiveText, refresh: () => void): void {
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
  bubble.querySelector('.body')?.replaceWith(liveBody(open, last.text, cache, refresh))
  if (box && atEnd) box.scrollTop = box.scrollHeight
}

/** Copies one message's Markdown as the agent wrote it, without the daemon's markers. */
function copyButton(text: string): HTMLElement {
  const b = el('button', { type: 'button', class: 'copy', text: 'Copy', 'aria-label': 'Copy this message' })
  b.addEventListener('click', () => {
    navigator.clipboard?.writeText(text.replace(TOOL_MARKERS, '')).then(
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

/** The row above the list: what is shown, what every conversation has cost in tokens, and the switch between open and all. */
function tools(s: State, actions: InboxActions, compact: boolean): HTMLElement {
  const all = conversationList(s)
  const open = all.filter(isOpen).length
  const over = all.length - open
  const seg = (which: 'open' | 'all', text: string): HTMLElement =>
    el('button', { type: 'button', class: 'btn small', 'aria-pressed': s.inboxFilter === which ? 'true' : 'false', text, onclick: () => actions.filter(which) })
  const count = compact ? null : s.inboxFilter === 'all' ? `${all.length} conversations` : `${open} open${over ? `, ${over} finished hidden` : ''}`
  // The total is over every conversation on every island, whatever the filter lists.
  const tokens = sumTokens(all)
  const spent = tokens.total ? el('span', { title: `${tokensBreakdown(tokens)}, across every conversation`, text: `${tokensText(tokens.total)} tokens${compact ? '' : ' used'}` }) : null
  const left = el('span', { class: 'muted' }, [count, count && spent ? ' · ' : null, spent])
  return el('div', { class: 'inbox-tools' }, [left, el('div', { class: 'modes', role: 'group', 'aria-label': 'Which conversations' }, [seg('open', 'Open'), seg('all', 'All')])])
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
      el('td', {}, [timeNode(x.activeAt, 'ago')]),
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
        el('span', { class: 'when' }, [timeNode(x.activeAt, 'ago')]),
      ],
    )
    box.appendChild(row)
  }
  return box
}

/** One conversation: its turns, the notes between them, how it ended, and the reply box while it is open. */
function conversation(s: State, x: Session, transcript: string | undefined, fetchError: string | undefined, changes: Fetched | undefined, cache: FetchCache, actions: InboxActions): HTMLElement {
  const status = sessionStatus(x, isWaiting(s, x))
  const repo = s.repos[x.repoId]
  const name = agentLabel(x.agent)
  const head = el('div', { class: 'convo-head' }, [
    el('h2', { text: sessionLabel(x), title: x.prompt }),
    el('span', { class: 'who muted' }, [agentGlyph(x.agent), `${name} \u00b7 ${repo?.name ?? x.repoId} on ${x.host}`]),
    el('span', { class: 'pill' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]),
    el('span', { class: 'muted', text: `${x.turns} ${x.turns === 1 ? 'turn' : 'turns'}` }),
    el('span', { class: 'muted', title: tokensBreakdown(x.tokens) || null, text: tokensLabel(x.tokens) }),
    el('div', { class: 'acts' }, [
      isOpen(x) ? trustSwitch(x, actions) : null,
      el('button', {
        type: 'button',
        class: 'btn small ghost',
        text: `Rules (${x.rules.length})`,
        'aria-expanded': rulesOpen.has(x.id) ? 'true' : 'false',
        onclick: () => {
          if (rulesOpen.has(x.id)) rulesOpen.delete(x.id)
          else rulesOpen.add(x.id)
          rulesToggles++
          actions.refresh()
        },
      }),
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
    else if (t === liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, t.text, cache, actions.refresh)]))
    else if (t.text) body.appendChild(el('div', { class: 'msg agent' }, [el('span', { class: 'from' }, [name, copyButton(t.text)]), agentBody(x, t.text, cache, actions.refresh)]))
  }
  if (running && !liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, '', cache, actions.refresh)]))
  for (const p of x.pending) body.appendChild(approvalCard(s, x, p, actions))
  for (const q of x.questions) body.appendChild(questionCard(x, q, actions))
  if (fetchError) body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: fetchError }))
  if (x.status === 'failed') body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: `Failed: ${x.error ?? 'unknown error'}` }))
  else if (x.status === 'ended') body.appendChild(el('p', { class: 'convo-end', text: x.stopReason === 'stopped' ? 'You ended this session.' : `This session ended (${(x.stopReason ?? 'done').replace(/_/g, ' ')}).` }))

  const foot = el('div', { class: 'convo-foot' })
  if (x.status !== 'running') foot.appendChild(changesCard(x, changes, actions))
  if (isOpen(x)) foot.appendChild(replyBox(x, { prompt: (text) => actions.prompt(x, text), cancel: () => actions.cancel(x), stop: () => actions.stop(x) }))
  else foot.appendChild(el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'This session is over. Start a new one from its terrace in the world.' }))

  const hint = isOpen(x) && x.trusted ? el('p', { class: 'trust-hint', text: 'Tool calls in this session run without asking.' }) : null
  const rules = rulesOpen.has(x.id)
    ? el('div', { class: 'convo-rules' }, [
        rulesSheet(x.rules, { defaults: HOST_DEFAULTS, copiedFrom: repo?.name ?? 'the repository', readOnly: !isOpen(x), onChange: (next) => actions.setSessionRules(x, next) }),
      ])
    : null
  const convo = el('section', { class: 'convo', 'aria-label': `${sessionLabel(x)}, with ${name}`, 'data-session': x.id }, [head, hint, rules, body, foot])
  return convo
}

/** "512 bytes", "12 KB", "3.4 MB". */
function sizeText(n: number): string {
  if (n < 1024) return `${n} bytes`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
