import { el } from './dom'
import type { FetchCache, Fetched } from './fetchcache'
import type { LiveText } from './live'
import { renderMarkdown } from './markdown'
import { agentGlyph, statusPill, timeNode } from './panels/parts'
import type { AgentAvailability, AgentCommand, Carried, Permission, Question, QuestionField, Session } from './protocol'
import { replyBox } from './reply'
import { imagesRefused } from './start'
import { imageThumb } from './thumbs'
import type { Turn } from './transcript'
import { inputsVersion, requestInput } from './requestinput'
import { canSign as canSignIn, myApprover } from './approvers'
import { HOST_DEFAULTS, proposeRule, ruleText, rulesSheet } from './rules'
import { agentLabel, isOpen, modelText, sessionLabel, sessionStatus, tokensBreakdown, tokensLabel, tokensText } from './status'
import { conversationList, isUnread, isWaiting, sumTokens, type State } from './store'
import { chipDeps, toolChip } from './toolchip'
import { canTerminal, convoMode, type ConvoMode, type Terminals } from './terminal'

import { parseTranscript } from './transcript'
import type { Rule } from './protocol'

export interface InboxActions {
  /** Opens one conversation, or none. */
  open(id: string | null): void
  /** Lists open conversations or all of them. */
  filter(which: 'open' | 'all'): void
  /** Opens the form that starts a session on any repository. */
  newSession(): void
  /** Shows the session in the world instead. */
  openInWorld(s: Session): void
  /** Shows a conversation's chat, or a terminal in its worktree, opening one the first time. */
  mode(s: Session, mode: ConvoMode): void
  /** Sends another prompt, with the files it mentions and the images uploaded for it. */
  prompt(s: Session, text: string, carried: Carried): Promise<string>
  /** Interrupts the turn in progress and keeps the session. */
  cancel(s: Session): Promise<string>
  /** The slash commands the session's agent announced, less the ones the host withholds, or an error message. */
  commands(s: Session): Promise<AgentCommand[] | string>
  /** Sets an idle session's mode, or its model. Resolves with an error message, or empty. */
  setMode(s: Session, mode: string): Promise<string>
  setModel(s: Session, model: string): Promise<string>
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
  /**
   * Merges the session's branch into its base in the checkout. after counts
   * the changes sent just before it, such as a commit, which have moved
   * the session's revision on from what it shows.
   */
  merge(s: Session, after?: number): Promise<string>
  /** Pushes the branch and opens a pull request from it. */
  pullRequest(s: Session, after?: number): Promise<string>
  /** Removes an ended session's worktree, and its branch when git finds it merged. */
  removeWorktree(s: Session): Promise<string>
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
export function renderInbox(root: HTMLElement, s: State, cache: FetchCache, live: LiveText, actions: InboxActions, terminals: Terminals): void {
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
  const deps = [s.sessions, s.repos, s.viewed, s.inbox, s.inboxFilter, open, inputsVersion(), s.approvers, s.me, s.world, fetched?.at, fetched?.loading, fetched?.error, fetched?.text, changes?.at, changes?.loading, changes?.error, changes?.changes, rulesToggles, open ? settingsKey(s, open) : '', open ? convoMode(s, open) : '', terminals.version(), ...(open ? chipDeps(open, cache) : [])]
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  lastDeps = deps

  const scrolls = new Map<string, { top: number; atEnd: boolean }>()
  for (const box of root.querySelectorAll<HTMLElement>('[data-scroll]')) {
    scrolls.set(box.dataset.scroll as string, { top: box.scrollTop, atEnd: box.scrollHeight - box.scrollTop - box.clientHeight < 12 })
  }
  const openedBefore = root.querySelector<HTMLElement>('.convo')?.dataset.session
  // A terminal is moved into the new page, not drawn again, and gets the
  // keyboard back if it had it.
  const typing = terminals.focused()

  root.textContent = ''
  root.appendChild(open ? narrowList(s, open.id, actions) : table(s, actions))
  if (open) {
    const { text, gap } = live.compose(open.id, fetched)
    if (gap && fetched && !fetched.loading) cache.get('transcript', open.host, open.id, 0, Date.now())
    root.appendChild(conversation(s, open, text, fetched?.error, changes, cache, actions, terminals))
  }
  terminals.reattached(typing)

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
export function approvalCard(s: State, x: Session, p: Permission, actions: InboxActions): HTMLElement {
  // The whole input, which the person sees and approves by its hash. Until
  // it is here and its hash checks out, nothing can be allowed.
  const view = requestInput(x.host, x.id, p, actions.redraw)
  // Only an approver's browser can answer: the daemon checks its signature.
  const me = myApprover(s)
  const canSign = !!me?.valid
  const note = el('span', { class: 'sub' })
  const buttons: HTMLButtonElement[] = []
  const enable = (): void => {
    for (const b of buttons) b.disabled = !canSign || (b.dataset.allows === 'true' && !view.hash)
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
  if (!canSign) {
    note.replaceChildren(
      me ? 'This browser no longer approves for this world, so it can’t answer here. ' : 'This browser isn’t an approver for this world, so it can’t answer here. ',
      'Add it with an \u201cAdd an approver\u201d link from an approver, use your recovery file on the ',
      el('a', { href: '/setup', text: 'setup page' }),
      ', or answer on the host with skylands request approve.',
    )
    note.style.color = ''
  }
  // Only the agent's "once" options: an "always" option would settle later
  // calls inside the agent, out of reach of the rules and of revoking an
  // approver, so the daemon refuses it.
  const once = p.options.find((o) => o.kind === 'allow_once')
  const rejects = p.options.filter((o) => o.kind === 'reject_once')
  if (!once && canSign) note.textContent = 'The agent offers no \u201callow once\u201d answer, so this can only be denied, or allowed by a rule.'
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
  const where = x.worktreeRemoved
    ? x.branchKept
      ? `The worktree was removed. ${x.branch} is kept: it has commits that aren’t merged.`
      : 'The worktree was removed.'
    : x.inCheckout
      ? `In the checkout, on ${x.branch}`
      : `On ${x.branch}${x.base ? `, from ${x.base}` : ''}`
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
      row.appendChild(button(`Commit and merge into ${x.base}`, true, () => void act(async () => (await commit()) || actions.merge(x, 1), 'Committing and merging\u2026')))
      if (!x.pullRequest) row.appendChild(button('Commit and open a pull request', false, () => void act(async () => (await commit()) || actions.pullRequest(x, 1), 'Committing and pushing\u2026')))
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
  // Once the session is over, its worktree can go, and its branch with it
  // when git finds it merged. Nothing uncommitted is ever removed.
  if ((x.status === 'ended' || x.status === 'failed') && !x.inCheckout && !dirty) {
    const keeps = c.ahead > 0 ? `${x.branch} stays: ${c.base} lacks its ${c.ahead === 1 ? 'commit' : `${c.ahead} commits`}.` : ''
    box.appendChild(el('div', { class: 'acts' }, [button('Remove worktree', false, () => void act(() => actions.removeWorktree(x), 'Removing…')), keeps ? el('span', { class: 'sub', text: keeps }) : null]))
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
  const input = el('input', { type: f.type === 'number' || f.type === 'integer' ? 'number' : 'text', step: f.type === 'integer' ? '1' : f.type === 'number' ? 'any' : null, name, 'aria-label': f.title ?? f.key, placeholder: f.description ?? '' }) as HTMLInputElement
  read.set(f.key, () => {
    const v = input.value.trim()
    if (v === '') return undefined
    return f.type === 'number' || f.type === 'integer' ? Number(v) : v
  })
  return el('div', { class: 'qfield' }, [label, input])
}

/** A question the agent is waiting on: its message, a control per field, and the answer or the refusal. */
export function questionCard(s: State, x: Session, q: Question, actions: InboxActions): HTMLElement {
  const note = el('span', { class: 'sub' })
  // Only an approver's browser can answer: the daemon checks its signature.
  const signs = canSignIn(s)
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
  if (!signs) {
    for (const b of buttons) b.disabled = true
    note.textContent = `${myApprover(s) ? 'This browser no longer approves for this world' : 'This browser isn\u2019t an approver for this world'}, so it can\u2019t answer here. Answer on the host with skylands request answer, or from an approver.`
  }
  return el('div', { class: 'approval question', role: 'group', 'aria-label': `The agent asks: ${q.message}` }, [
    el('div', { class: 'ask' }, [el('span', { class: 'mark', style: 'background:#E0B000' }), el('strong', { text: q.message }), el('span', { class: 'muted', style: 'margin-left:auto;font-size:12px' }, ['asked ', timeNode(q.at, 'ago')])]),
    ...fields,
    el('div', { class: 'acts' }, [answer, decline, note]),
  ])
}

/** What the island says about the session's agent: the modes and models it offers. */
function agentOf(s: State, x: Session): AgentAvailability | undefined {
  return s.islands[x.host]?.agents?.find((a) => a.name === x.agent)
}

/** What the setting chips show for the open session, so the conversation is rebuilt when it changes. */
function settingsKey(s: State, x: Session): string {
  const a = agentOf(s, x)
  return JSON.stringify([a?.modes ?? [], a?.models ?? [], a?.modelsRead ?? false])
}

/**
 * The open session's mode and model, as chips that change them while the
 * agent waits for a reply. The mode chip lists the modes the adapter
 * lists, and shows only when there is more than one; the model chip lists
 * the models the island's login offers, with the session's own kept.
 */
function settingChips(s: State, x: Session, actions: InboxActions): HTMLElement[] {
  const a = agentOf(s, x)
  const idle = x.status === 'idle'
  const why = idle ? '' : 'Changes once the agent is waiting for you'
  const chip = (label: string, current: string, options: { id: string; name?: string; description?: string }[], set: (v: string) => Promise<string>): HTMLElement => {
    const sel = el('select', { 'aria-label': label, disabled: idle ? null : 'true', title: why || null }) as HTMLSelectElement
    sel.replaceChildren(...options.map((o) => el('option', { value: o.id, text: label === 'Model' ? modelText(o) : o.name || o.id, title: o.description || null })))
    sel.value = current
    const box = el('label', { class: 'chip' }, [label, sel])
    sel.addEventListener('change', async () => {
      sel.disabled = true
      const err = await set(sel.value)
      sel.disabled = false
      if (err) {
        sel.value = current
        box.title = err
      }
    })
    return box
  }
  const out: HTMLElement[] = []
  const modes = a?.modes ?? []
  if (modes.length > 1) out.push(chip('Mode', x.mode || modes[0].id, modes, (v) => actions.setMode(x, v)))
  const models = a?.models ?? []
  if (models.length) {
    const options = models.some((m) => m.id === x.model) ? models : [{ id: x.model ?? '', name: x.model ? x.model : `Agent’s default${a?.defaultModel ? ` (${a.defaultModel})` : ''}` }, ...models]
    out.push(chip('Model', x.model ?? '', options, (v) => actions.setModel(x, v)))
  }
  return out
}

/** The switch that lets a session's tool calls through without asking, unless a deny rule refuses them. */
function trustSwitch(s: State, x: Session, actions: InboxActions): HTMLElement {
  const input = el('input', { type: 'checkbox', 'aria-label': 'Trust this session' }) as HTMLInputElement
  input.checked = !!x.trusted
  const signs = canSignIn(s)
  input.disabled = !signs
  const title = signs ? 'Allow every tool call in this session without asking, unless a deny rule refuses it' : 'Only an approver for this world can trust a session'
  const label = el('label', { class: 'trust', title }, [input, 'Trust this session'])
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

/** The row above the list: what is shown, what every conversation has cost in tokens, the switch between open and all, and New. */
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
  const which = el('div', { class: 'modes', role: 'group', 'aria-label': 'Which conversations' }, [seg('open', 'Open'), seg('all', 'All')])
  const start = el('button', { type: 'button', class: 'btn small primary', text: 'New', title: 'Start a session on any repository', onclick: () => actions.newSession() })
  return el('div', { class: 'inbox-tools' }, [left, el('div', { class: 'acts' }, [which, start])])
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
    box.appendChild(el('p', { class: 'inbox-empty', text: any ? 'No open conversations. Switch to All to see finished ones.' : 'No conversations yet. Press New to give an agent some work.' }))
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

/** The switch between a conversation's chat and a terminal in its worktree, shown while it can have one. */
function modeSwitch(s: State, x: Session, actions: InboxActions): HTMLElement | null {
  if (!canTerminal(x)) return null
  const mode = convoMode(s, x)
  const seg = (which: ConvoMode, text: string, title: string): HTMLElement =>
    el('button', { type: 'button', class: 'btn small', 'aria-pressed': mode === which ? 'true' : 'false', text, title, onclick: () => actions.mode(x, which) })
  return el('div', { class: 'modes', role: 'group', 'aria-label': 'Show' }, [
    seg('chat', 'Chat', 'The conversation with the agent'),
    seg('terminal', 'Terminal', `A shell where the agent works, on ${x.host}`),
  ])
}

/** The program a shell path names, such as zsh for /bin/zsh. */
function shellName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1) || path
}

/**
 * The conversation's terminal: a shell in the session's worktree on its
 * island, with a tab for each when more than one is open, and the way to
 * open another or close this one.
 */
function terminalPane(s: State, x: Session, terminals: Terminals): HTMLElement {
  const v = terminals.view(x)
  const where = x.inCheckout ? `the checkout of ${s.repos[x.repoId]?.name ?? x.repoId}` : `the worktree on ${x.branch}`
  const bar = el('div', { class: 'term-bar' })
  const pane = el('section', { class: 'term-pane', 'aria-label': `Terminal in ${where} on ${x.host}` }, [bar])
  if (x.terminals.length > 1) {
    bar.appendChild(
      el(
        'div',
        { class: 'modes', role: 'group', 'aria-label': 'Terminals' },
        x.terminals.map((t, i) => el('button', { type: 'button', class: 'btn small', 'aria-pressed': v.current?.id === t.id ? 'true' : 'false', text: `${i + 1}`, title: `${shellName(t.shell)}, opened ${new Date(t.openedAt).toLocaleTimeString()}`, onclick: () => terminals.choose(x, t.id) })),
      ),
    )
  }
  const open = el('button', { type: 'button', class: 'btn small ghost', text: v.current ? 'New terminal' : 'Open a terminal', disabled: v.opening ? 'true' : null, onclick: () => void terminals.open(x) })
  const canOpen = x.status === 'running' || x.status === 'idle'
  if (!v.current) {
    const what = `A shell in ${where} on ${x.host}`
    bar.appendChild(el('span', { class: 'muted', text: what, title: what }))
    const say = v.opening ? 'Opening a terminal…' : v.error || (canOpen ? 'No terminal is open in this session.' : 'This session is over, so no terminal can open in it.')
    pane.appendChild(el('div', { class: 'term-empty' }, [el('p', { text: say, style: v.error && !v.opening ? 'color:var(--bad)' : null }), canOpen && !v.opening ? open : null]))
    return pane
  }
  const t = v.current
  const note = el('span', { class: 'sub', style: 'color:var(--bad)' })
  const close = el('button', {
    type: 'button',
    class: 'btn small ghost',
    text: 'Close terminal',
    title: 'End this shell and whatever runs in it',
    onclick: async () => {
      close.disabled = true
      const err = await terminals.close(x, t.id)
      if (err) {
        note.textContent = err
        note.title = err
        close.disabled = false
      }
    },
  }) as HTMLButtonElement
  const what = `${shellName(t.shell)} in ${where} on ${x.host}`
  bar.append(el('span', { class: 'muted', text: what, title: what }), note, el('div', { class: 'acts' }, [canOpen ? open : null, close]))
  pane.appendChild(terminals.screen(x, t.id))
  const trouble = terminals.note(x, t.id)
  if (trouble) pane.appendChild(el('p', { class: 'term-note', text: trouble }))
  return pane
}

/** One conversation: its turns, the notes between them, how it ended, and the reply box while it is open. */
function conversation(s: State, x: Session, transcript: string | undefined, fetchError: string | undefined, changes: Fetched | undefined, cache: FetchCache, actions: InboxActions, terminals: Terminals): HTMLElement {
  const status = sessionStatus(x, isWaiting(s, x))
  const repo = s.repos[x.repoId]
  const name = agentLabel(x.agent)
  const running = x.status === 'running'
  // The head is the session's, the same in chat and in the terminal, so
  // nothing in it moves when the mode does: what the session is on the
  // left, with the status last because its width changes most, and the
  // switch and the ways out on the right. What only one mode has goes in
  // that mode's bar under the head.
  const head = el('div', { class: 'convo-head' }, [
    el('div', { class: 'convo-id' }, [
      el('h2', { text: sessionLabel(x), title: x.prompt }),
      el('div', { class: 'convo-facts' }, [
        el('span', { class: 'who muted' }, [agentGlyph(x.agent), el('span', { class: 'what', text: `${name} \u00b7 ${repo?.name ?? x.repoId} on ${x.host}` })]),
        el('span', { class: 'muted', text: `${x.turns} ${x.turns === 1 ? 'turn' : 'turns'}` }),
        el('span', { class: 'muted', title: tokensBreakdown(x.tokens, running) || null, text: tokensLabel(x.tokens, running) }),
        statusPill(status),
      ]),
    ]),
    el('div', { class: 'acts' }, [
      modeSwitch(s, x, actions),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Show in world', onclick: () => actions.openInWorld(x) }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Close', 'aria-label': 'Close the conversation', onclick: () => actions.open(null) }),
    ]),
  ])
  if (convoMode(s, x) === 'terminal') {
    return el('section', { class: 'convo', 'aria-label': `${sessionLabel(x)}, with ${name}`, 'data-session': x.id }, [head, terminalPane(s, x, terminals)])
  }

  const body = el('div', { class: 'convo-body', 'data-scroll': 'convo' })
  const conv = parseTranscript(transcript ?? '')
  if (!conv.turns.length) conv.turns.push({ who: 'you', text: x.prompt })
  // While a turn runs, its last agent turn is the live bubble, which grows
  // in place as the feed arrives rather than rebuilding the conversation.
  const last = conv.turns[conv.turns.length - 1]
  const liveTurn = running && last?.who === 'agent' ? last : null
  for (const t of conv.turns) {
    if (t.who === 'you') body.appendChild(el('div', { class: 'msg you' }, [el('span', { class: 'from', text: 'You' }), el('div', { class: 'body', text: t.text }), carriedRow(x, t)]))
    else if (t.who === 'note') body.appendChild(el('p', { class: 'convo-note', text: t.text }))
    else if (t === liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, t.text, cache, actions.refresh)]))
    else if (t.text) body.appendChild(el('div', { class: 'msg agent' }, [el('span', { class: 'from' }, [name, copyButton(t.text)]), agentBody(x, t.text, cache, actions.refresh)]))
  }
  if (running && !liveTurn) body.appendChild(el('div', { class: 'msg agent live' }, [el('span', { class: 'from', text: name }), liveBody(x, '', cache, actions.refresh)]))
  for (const p of x.pending) body.appendChild(approvalCard(s, x, p, actions))
  for (const q of x.questions) body.appendChild(questionCard(s, x, q, actions))
  if (fetchError) body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: fetchError }))
  if (x.status === 'failed') body.appendChild(el('p', { class: 'convo-end', style: 'color:var(--bad)', text: `Failed: ${x.error ?? 'unknown error'}` }))
  else if (x.status === 'ended') body.appendChild(el('p', { class: 'convo-end', text: x.stopReason === 'stopped' ? 'You ended this session.' : `This session ended (${(x.stopReason ?? 'done').replace(/_/g, ' ')}).` }))

  const foot = el('div', { class: 'convo-foot' })
  if (x.status !== 'running') foot.appendChild(changesCard(x, changes, actions))
  if (isOpen(x))
    foot.appendChild(
      replyBox(x, {
        prompt: (text, carried) => actions.prompt(x, text, carried),
        noImages: () => imagesRefused(s.islands[x.host], x.agent),
        commands: () => actions.commands(x),
        cancel: () => actions.cancel(x),
        stop: () => actions.stop(x),
      }),
    )
  else foot.appendChild(el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'This session is over. Press New to start another.' }))

  const hint = !isOpen(x)
    ? null
    : x.trusted
      ? el('p', { class: 'trust-hint', text: 'Tool calls in this session run without asking, unless a deny rule refuses them.' })
      : x.trustedBy === 'revocation'
        ? el('p', { class: 'trust-hint ended', text: `Trust ended when ${approverName(s, x.trustApprover)} stopped approving for this world. Tool calls ask again.` })
        : null
  const rules = rulesOpen.has(x.id)
    ? el('div', { class: 'convo-rules' }, [
        rulesSheet(x.rules, {
          defaults: HOST_DEFAULTS,
          copiedFrom: repo?.name ?? 'the repository',
          readOnly: !isOpen(x) || !canSignIn(s),
          lapsed: isOpen(x) && x.rulesLapsed ? lapsedText(s, x.rulesApprover, x.rulesBy) : undefined,
          onChange: (next) => actions.setSessionRules(x, next),
        }),
      ])
    : null
  const convo = el('section', { class: 'convo', 'aria-label': `${sessionLabel(x)}, with ${name}`, 'data-session': x.id }, [head, chatBar(s, x, actions), hint, rules, body, foot])
  return convo
}

/**
 * The chat's bar, where the terminal has its own: the mode and model
 * chips and the trust switch while the session is open, and its rules.
 */
function chatBar(s: State, x: Session, actions: InboxActions): HTMLElement {
  const rules = el('button', {
    type: 'button',
    class: 'btn small ghost',
    text: isOpen(x) && x.rulesLapsed ? `Rules (${x.rules.length}, lapsed)` : `Rules (${x.rules.length})`,
    'aria-expanded': rulesOpen.has(x.id) ? 'true' : 'false',
    onclick: () => {
      if (rulesOpen.has(x.id)) rulesOpen.delete(x.id)
      else rulesOpen.add(x.id)
      rulesToggles++
      actions.refresh()
    },
  })
  return el('div', { class: 'chat-bar' }, [...(isOpen(x) ? settingChips(s, x, actions) : []), isOpen(x) ? trustSwitch(s, x, actions) : null, el('div', { class: 'acts' }, [rules])])
}

/** Under a prompt, the files it mentioned as chips and the images it carried as thumbnails, or nothing. */
function carriedRow(x: Session, t: Turn): HTMLElement | null {
  if (!t.mentions?.length && !t.images?.length) return null
  return el('div', { class: 'carried' }, [
    ...(t.mentions ?? []).map((m) =>
      el('span', {
        class: `mention-chip${m.embedded ? '' : ' linked'}`,
        text: `@${m.path}`,
        title: m.embedded ? 'The agent got this file’s contents as they were when you sent the prompt' : 'The agent got a link to this file and reads it itself',
      }),
    ),
    ...(t.images ?? []).map((i) => imageThumb(x.host, x.id, i.name, i.hash)),
  ])
}

/** An approver's name as the registry has it, for saying whose approval lapsed. */
export function approverName(s: State, key: string | undefined): string {
  return (key && s.approvers?.find((a) => a.key === key)?.name) || 'an approver'
}

/** Why a rule list lapsed, for its rules sheet. */
export function lapsedText(s: State, approver: string | undefined, by: string | undefined): string {
  const who = approver ? `${approverName(s, approver)}, who set them, no longer approves for this world` : by === 'citadel' ? 'They were set before this world had approvers' : 'Nobody who approves for this world set them'
  return `${who}, so their allow rules ask until an approver approves them again.`
}

/** "512 bytes", "12 KB", "3.4 MB". */
function sizeText(n: number): string {
  if (n < 1024) return `${n} bytes`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
