import '@fontsource/barlow/400.css'
import '@fontsource/barlow/500.css'
import '@fontsource/barlow/600.css'
import '@fontsource/barlow/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/sora/700.css'
import './style.css'

import { CommandBar } from './bar'
import { byId, el } from './dom'
import { FetchCache } from './fetchcache'
import { renderHeader, type NavActions } from './header'
import { renderAgentPanel, type AgentActions } from './panels/agent'
import { renderFilePanel } from './panels/file'
import { refreshTimes } from './panels/parts'
import { renderIslandPanel } from './panels/island'
import { type PruneOptions, pruneVersion } from './panels/prune'
import { renderTerracePanel } from './panels/terrace'
import { renderWorldPanel } from './panels/world'
import { agentLabel, citadelLabelStatus, clockText, durationText, islandStatus, isStale, secondsSince } from './status'
import { islandList, isWaiting, level, pendingCount, pendingItems, reposOn, sessionsOn, Store, waitingCount, type State } from './store'
import { growLive, lapsedText, renderInbox, type InboxActions } from './inbox'
import { LiveText } from './live'
import { NewSessionForm } from './newsession'
import { refreshOpenChips } from './toolchip'
import { renderMarkdown } from './markdown'
import { providerOf } from './world/airship'
import type { AgentState } from './world/agents'
import type { AgentCommand, Rule } from './protocol'
import type { World } from './world/world'
import { connectToCitadel } from './ws'
import { api } from './signin'
import { approverActions, canSign, keepThisBrowser, loadThisBrowser, signAnswer, signChange, signFor, signPrompt, signRepoRules, signSessionRules, signTerminal, signTerminalInput, signTrust } from './approvers'
import { terminalInSight, Terminals } from './terminal'
import { pullRequestHash, textHash } from './decision'
import { browserNotices, Notifier, notifySupport, waitingItems } from './notify'
import { closeStale, pushSupport, subscribePush, unsubscribePush } from './push'
import { installApp, watchInstall } from './install'
import { renderPhone, type PhoneActions } from './phone'
import { inboxPath, parseInboxPath, type InboxRoute } from './route'

const stage = byId<HTMLElement>('stage')
const canvas = byId<HTMLCanvasElement>('world')
const labelsEl = byId<HTMLElement>('labels')
const panelEl = byId<HTMLElement>('panel')
const crumbsEl = byId<HTMLElement>('crumbs')
const connectedEl = byId<HTMLElement>('connected')
const versionEl = byId<HTMLElement>('version')
const linkEl = byId<HTMLElement>('link')
const veilEl = byId<HTMLElement>('veil')
const veilMsgEl = byId<HTMLElement>('veilmsg')
const inboxEl = byId<HTMLElement>('inbox')
const modeWorld = byId<HTMLButtonElement>('modeWorld')
const modeInbox = byId<HTMLButtonElement>('modeInbox')
const inboxCount = byId<HTMLElement>('inboxCount')
const appEl = byId<HTMLElement>('app')
const phoneEl = byId<HTMLElement>('phone')

const store = new Store()
const approverActs = approverActions(store)
void loadThisBrowser(store)
// The world's description needs no sign-in, so the header can name the
// citadel's version before this browser signs in, and in one that can't.
void fetch('/api/world')
  .then((resp) => (resp.ok ? (resp.json() as Promise<{ version?: string }>) : null))
  .then((w) => {
    if (w?.version) store.update({ citadelVersion: w.version })
  })
  .catch(() => {})
const bar = new CommandBar()
// A session started from the inbox's New form opens there.
const newSession = new NewSessionForm((id) => openConversation(id))
const cache = new FetchCache(() => store.touch())
const live = new LiveText()

// Terminals in sessions' worktrees, each signed for by this browser: the
// opening and every keystroke.
const terminals = new Terminals({
  signOpen: (x, terminal) => signTerminal(store.state, x, terminal),
  signInput: (x, terminal, attach, seq, data) => signTerminalInput(store.state, x, terminal, attach, seq, data),
  api,
  redraw: () => store.touch(),
})

// Live pieces grow the open conversation's last bubble, at most once a frame.
let growQueued = false
const link = connectToCitadel(
  store,
  (_host, id, piece) => {
    live.push(id, piece)
    if (growQueued) return
    growQueued = true
    requestAnimationFrame(() => {
      growQueued = false
      growLive(inboxEl, store.state, cache, live, () => store.touch())
    })
  },
  { output: (t, out) => terminals.output(t, out), reconnected: () => terminals.reconnected() },
)
terminals.link = link

// The 3D world is loaded and built only for a screen that shows it, so a
// phone never downloads it.
let world: World | null = null
let worldAsked = false
async function ensureWorld(): Promise<void> {
  if (worldAsked) return
  worldAsked = true
  let made: World
  try {
    const { World } = await import('./world/world')
    try {
      made = new World(stage, canvas, labelsEl, store.state.worldTheme)
    } catch (err) {
      console.error('world: WebGL unavailable', err)
      stage.appendChild(el('div', { class: 'nogl', text: 'This view needs WebGL, and it isn’t available in this browser right now.' }))
      return
    }
  } catch (err) {
    console.error('world: could not load', err)
    stage.appendChild(el('div', { class: 'nogl', text: `The 3D world couldn’t be loaded: ${(err as Error).message}. Reload the page to try again.` }))
    return
  }
  world = made
  world.onNav = (n) => store.update(n)
  world.start()
  store.touch()
}

// Navigation goes through the world when it runs, so the camera moves, and
// straight to the store otherwise.
const nav: NavActions = {
  overview: () => (world ? world.overview() : store.update({ selected: 'cit', terrace: null, file: null, agent: null })),
  select: (host) => (world ? world.select(host) : store.update({ selected: host, terrace: null, file: null, agent: null })),
  land: (host, terrace) => (world ? world.land(host, terrace) : store.update({ selected: host, terrace, file: null, agent: null })),
  selectAgent: (host, terrace, agent) => (world ? world.selectAgent(host, terrace, agent) : store.update({ selected: host, terrace, file: null, agent })),
}

/** Posts a command for one session to the citadel and returns an error message, or empty. */
async function sessionCommand(host: string, id: string, action: string, body: unknown): Promise<string> {
  try {
    const resp = await api(`/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(id)}/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (resp.ok) return ''
    const out = (await resp.json().catch(() => ({}))) as { error?: string }
    return out.error ?? `The citadel answered ${resp.status}.`
  } catch (err) {
    return `Could not reach the citadel: ${(err as Error).message}`
  }
}

const inboxActions: InboxActions = {
  open: (id) => store.update({ inbox: id }),
  filter: (which) => store.setInboxFilter(which),
  newSession: () => newSession.open(store.state),
  openInWorld: (x) => {
    store.setView('world')
    nav.selectAgent(x.host, x.repoId, x.id)
  },
  // The first switch to a terminal opens one; after that it shows what is open.
  mode: (x, mode) => {
    store.update({ convoMode: { ...store.state.convoMode, [x.id]: mode } })
    if (mode === 'terminal' && !x.terminals.length) void terminals.open(x)
  },
  prompt: (x, text) => {
    const revision = x.revision ?? 0
    const signed = signPrompt(store.state, x, text, revision)
    if (signed.error) return Promise.resolve(signed.error)
    return sessionCommand(x.host, x.id, 'prompt', { text, revision, signature: signed.signature })
  },
  cancel: (x) => sessionCommand(x.host, x.id, 'cancel', {}),
  setMode: (x, mode) => sessionCommand(x.host, x.id, 'mode', { mode }),
  commands: async (x) => {
    try {
      const resp = await api(`/api/sessions/${encodeURIComponent(x.host)}/${encodeURIComponent(x.id)}/commands`)
      const out = (await resp.json().catch(() => ({}))) as { error?: string; commands?: AgentCommand[] }
      if (!resp.ok) return out.error ?? `The citadel answered ${resp.status}.`
      return out.commands ?? []
    } catch (err) {
      return `Could not reach the citadel: ${(err as Error).message}`
    }
  },
  setModel: (x, model) => sessionCommand(x.host, x.id, 'model', { model }),
  stop: (x) => sessionCommand(x.host, x.id, 'stop', {}),
  redraw: () => store.update({}),
  approve: (x, p, optionId, inputHash) => {
    const signature = signFor(store.state, x, p, 'allow', optionId, inputHash)
    if (typeof signature === 'string') return Promise.resolve(signature)
    return sessionCommand(x.host, x.id, `requests/${encodeURIComponent(p.requestId)}/approve`, { optionId, inputHash, signature })
  },
  // A denial signs the input as the request was recorded: refusing needs no
  // look at the whole input.
  deny: (x, p) => {
    const signature = signFor(store.state, x, p, 'deny', '', p.inputHash)
    if (typeof signature === 'string') return Promise.resolve(signature)
    return sessionCommand(x.host, x.id, `requests/${encodeURIComponent(p.requestId)}/deny`, { signature })
  },
  // An answer signs the question as shown and the answers' canonical JSON.
  answer: (x, q, answers) => {
    const signature = signAnswer(store.state, x, q, answers)
    if (typeof signature === 'string') return Promise.resolve(signature)
    return sessionCommand(x.host, x.id, `questions/${encodeURIComponent(q.questionId)}/answer`, { answers, signature })
  },
  decline: (x, q) => {
    const signature = signAnswer(store.state, x, q, null)
    if (typeof signature === 'string') return Promise.resolve(signature)
    return sessionCommand(x.host, x.id, `questions/${encodeURIComponent(q.questionId)}/answer`, { decline: true, signature })
  },
  // Trust and rules are signed over the revision this browser shows, so
  // a change made since is refused rather than overwritten.
  trust: (x, trusted) => {
    const signature = signTrust(store.state, x, trusted)
    if (typeof signature === 'string') return Promise.resolve(signature)
    return sessionCommand(x.host, x.id, 'trust', { trusted, revision: x.trustRevision ?? 0, signature })
  },
  commit: (x, message) => {
    const revision = x.revision ?? 0
    const signed = signChange(store.state, x, 'commit', textHash(message), revision)
    if (signed.error) return Promise.resolve(signed.error)
    return sessionCommand(x.host, x.id, 'commit', { message, revision, signature: signed.signature })
  },
  merge: (x, after = 0) => {
    const revision = (x.revision ?? 0) + after
    const signed = signChange(store.state, x, 'merge', '', revision)
    if (signed.error) return Promise.resolve(signed.error)
    return sessionCommand(x.host, x.id, 'merge', { revision, signature: signed.signature })
  },
  pullRequest: (x, after = 0) => {
    const revision = (x.revision ?? 0) + after
    const signed = signChange(store.state, x, 'pull-request', pullRequestHash('', ''), revision)
    if (signed.error) return Promise.resolve(signed.error)
    return sessionCommand(x.host, x.id, 'pull-request', { revision, signature: signed.signature })
  },
  removeWorktree: (x) => {
    const revision = x.revision ?? 0
    const signed = signChange(store.state, x, 'remove', '', revision)
    if (signed.error) return Promise.resolve(signed.error)
    return sessionCommand(x.host, x.id, 'remove-worktree', { revision, signature: signed.signature })
  },
  setSessionRules: (x, rules) => {
    const signed = signSessionRules(store.state, x, rules)
    if (typeof signed.signature === 'string') return Promise.resolve(signed.signature)
    return sessionCommand(x.host, x.id, 'rules', signed)
  },
  setRepoRules: (host, repoId, rules) => setRepoRules(host, repoId, rules),
  refresh: () => store.touch(),
}

/** How a panel removes what ended sessions left behind on its host or repository. */
function pruneOptions(s: State, scope: string, down: boolean): PruneOptions {
  return { scope, canSign: canSign(s), down, remove: (x) => inboxActions.removeWorktree(x), redraw: () => store.touch() }
}

/** Replaces a repository's rules, signed over the revision this browser shows. */
function setRepoRules(host: string, repoId: string, rules: Rule[]): Promise<string> {
  const repo = store.state.repos[repoId]
  if (!repo) return Promise.resolve('This browser doesn’t know that repository yet.')
  const signed = signRepoRules(store.state, repo, rules)
  if (typeof signed.signature === 'string') return Promise.resolve(signed.signature)
  return repoCommand(host, repoId, 'rules', signed)
}

/** Posts a command for one repository to the citadel and returns an error message, or empty. */
async function repoCommand(host: string, id: string, action: string, body: unknown): Promise<string> {
  try {
    const resp = await api(`/api/repos/${encodeURIComponent(host)}/${encodeURIComponent(id)}/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (resp.ok) return ''
    const out = (await resp.json().catch(() => ({}))) as { error?: string }
    return out.error ?? `The citadel answered ${resp.status}.`
  } catch (err) {
    return `Could not reach the citadel: ${(err as Error).message}`
  }
}

/** Shows the world or the inbox, and updates the switch in the header. */
function renderMode(s: State): void {
  const inbox = s.view === 'inbox'
  inboxEl.hidden = !inbox
  stage.hidden = inbox
  panelEl.hidden = inbox
  crumbsEl.hidden = inbox
  modeWorld.setAttribute('aria-pressed', inbox ? 'false' : 'true')
  modeInbox.setAttribute('aria-pressed', inbox ? 'true' : 'false')
  const waiting = waitingCount(s)
  inboxCount.hidden = waiting === 0
  inboxCount.textContent = String(waiting)
  document.title = waiting ? `(${waiting}) Skylands` : 'Skylands'
  world?.setPaused(inbox)
}

function renderVeil(s: State): void {
  // Only a lost link veils the world. Before the first contact there is nothing stale to show.
  veilEl.hidden = s.link !== 'lost'
  if (veilEl.hidden) return
  veilMsgEl.textContent = ''
  const since = s.lastContact ? `for ${durationText(secondsSince(s.lastContact))}` : ''
  veilMsgEl.append(
    `Citadel unreachable ${since}`.trim(),
    el('span', {
      text: s.lastContact
        ? `The world shows the last state seen, at ${clockText(s.lastContact)}. This page keeps retrying.`
        : 'This page keeps retrying.',
    }),
  )
}

/**
 * What the open panel is built from. The store replaces a record's object
 * only when that record changes, so comparing these by identity tells
 * whether the panel needs rebuilding at all.
 */
function panelDeps(s: State, stale: boolean): unknown[] {
  const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
  const repo = s.terrace ? s.repos[s.terrace] : undefined
  switch (level(s)) {
    case 'agent': {
      const ag = s.agent ? s.sessions[s.agent] : undefined
      const diff = island && ag ? cache.peek('diff', island.host, ag.id) : undefined
      return ['agent', ag, repo, island, stale, s.viewed, diff?.at, diff?.loading, diff?.error, diff?.text]
    }
    case 'file':
      return ['file', repo, island, s.file]
    case 'terrace':
      return ['terrace', repo, island, stale, s.viewed, s.approvers, s.me, s.world, pruneVersion(), ...(repo ? sessionsOn(s, repo.id) : [])]
    case 'island':
      return ['island', island, stale, stale ? s.lastContact : 0, s.approvers, s.me, s.world, pruneVersion(), ...(island ? reposOn(s, island.host) : []), ...Object.values(s.sessions).filter((x) => x.host === island?.host)]
    default:
      return ['world', s.server, s.world, s.link, s.worldTheme, s.notify, s.push, s.pushError, s.install, notifySupport() === 'ok' ? Notification.permission : '', waitingCount(s), s.approvers, s.me, s.persisted, s.invite, s.revoking, s.approverError, s.invite ? Date.now() > Date.parse(s.invite.expires) : false, ...islandList(s)]
  }
}

let lastDeps: unknown[] | null = null

/**
 * Rebuilds the panel when what it shows has changed, and leaves it alone
 * otherwise. Scroll positions of the panel and of any scrollable box inside
 * it survive a rebuild, so reading a long response is never interrupted.
 */
function renderPanel(s: State, stale: boolean): void {
  const deps = panelDeps(s, stale)
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  lastDeps = deps
  const panelScroll = panelEl.scrollTop
  const boxScrolls = new Map<string, number>()
  for (const box of panelEl.querySelectorAll<HTMLElement>('[data-scroll]')) boxScrolls.set(box.dataset.scroll as string, box.scrollTop)
  renderPanelContent(s, stale)
  panelEl.scrollTop = panelScroll
  for (const box of panelEl.querySelectorAll<HTMLElement>('[data-scroll]')) {
    const top = boxScrolls.get(box.dataset.scroll as string)
    if (top !== undefined) box.scrollTop = top
  }
}

function renderPanelContent(s: State, stale: boolean): void {
  const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
  const repo = s.terrace ? s.repos[s.terrace] : undefined
  switch (level(s)) {
    case 'agent': {
      const ag = s.agent ? s.sessions[s.agent] : undefined
      if (island && repo && ag) {
        const actions: AgentActions = {
          back: () => nav.land(island.host, repo.id),
          conversation: () => {
            store.update({ inbox: ag.id })
            store.setView('inbox')
          },
        }
        renderAgentPanel(panelEl, ag, repo, island, stale, isWaiting(s, ag), cache, actions)
        return
      }
      break
    }
    case 'file':
      if (island && repo && s.file !== null) {
        renderFilePanel(panelEl, repo, island, s.file, () => nav.land(island.host, repo.id))
        return
      }
      break
    case 'terrace':
      if (island && repo) {
        renderTerracePanel(
          panelEl,
          repo,
          island,
          stale,
          sessionsOn(s, repo.id),
          (x) => isWaiting(s, x),
          () => nav.select(island.host),
          (rules) => setRepoRules(island.host, repo.id, rules),
          canSign(s),
          lapsedText(s, repo.rulesApprover, repo.rulesBy),
          pruneOptions(s, `${island.host}/${repo.id}`, stale || !island.reachable),
        )
        return
      }
      break
    case 'island':
      if (island) {
        const sessions = Object.values(s.sessions).filter((x) => x.host === island.host)
        renderIslandPanel(panelEl, island, reposOn(s, island.host), stale, s.lastContact, sessions, (terrace) => nav.land(island.host, terrace), pruneOptions(s, island.host, stale || !island.reachable))
        return
      }
      break
  }
  renderWorldPanel(panelEl, s, nav.select, (id) => store.setWorldTheme(id), (on) => void turnNotifications(on), approverActs, browserActs)
}

/** Opens a conversation in the inbox, as clicking its notification does. */
function openConversation(id: string): void {
  store.update({ inbox: id })
  store.setView('inbox')
}

// A phone's screen gets only what waits on a person: a window is a phone's
// when it is narrow, or short, as a phone on its side is.
const phoneQuery = matchMedia('(max-width: 700px), (max-height: 500px)')

/**
 * Lays the page out for its screen, and opens it where its address says.
 * On a phone the address follows the view. The desktop keeps none, so a
 * link into the inbox opens its conversation there and leaves "/".
 */
function fitScreen(): void {
  const route = parseInboxPath(location.pathname)
  if (phoneQuery.matches) {
    store.update({ phone: true, route: route ?? {} })
    return
  }
  store.update({ phone: false })
  if (route) {
    history.replaceState(null, '', '/')
    store.update({ inbox: route.session ?? null })
    store.setView('inbox')
  }
  void ensureWorld()
}

/** Moves the phone's view to a route, as a new entry in the browser's history. */
function go(route: InboxRoute): void {
  history.pushState({ from: location.pathname }, '', inboxPath(route))
  store.update({ route })
}

/** Back to the list of what's waiting: the entry before, when that was it, or the list in place. */
function back(): void {
  const from = (history.state as { from?: string } | null)?.from
  if (from === '/' || from === '/inbox') {
    history.back()
    return
  }
  history.replaceState(null, '', '/inbox')
  store.update({ route: {} })
}

window.addEventListener('popstate', () => {
  if (store.state.phone) store.update({ route: parseInboxPath(location.pathname) ?? {} })
})
phoneQuery.addEventListener('change', fitScreen)

/** An answer sent from the phone's view goes back to the list. */
function thenBack(err: string): string {
  if (!err && store.state.phone) back()
  return err
}

const phoneActions: PhoneActions = {
  ...inboxActions,
  approve: (x, p, optionId, inputHash) => inboxActions.approve(x, p, optionId, inputHash).then(thenBack),
  deny: (x, p) => inboxActions.deny(x, p).then(thenBack),
  answer: (x, q, answers) => inboxActions.answer(x, q, answers).then(thenBack),
  decline: (x, q) => inboxActions.decline(x, q).then(thenBack),
  go,
  back,
  setNotify: (on) => void turnNotifications(on),
}

// The browser's offer to install the citadel as an app, and the button that takes it up.
watchInstall((install) => store.update({ install }))
const browserActs = {
  keep: () => void keepThisBrowser(store),
  install: () =>
    void installApp().then((installed) => {
      // Installing says so through the browser's own event. Declined, the
      // offer is spent until the browser makes another.
      if (!installed) store.update({ install: 'no' })
    }),
}

/** Opens what an inbox link names, as clicking a notification does. */
function openRoute(route: InboxRoute): void {
  if (store.state.phone) go(route)
  else openConversation(route.session ?? '')
}

// While notifications come as pushes, the service worker shows them, and
// the page shows none of its own.
const notifier = new Notifier(browserNotices(() => store.state.notify && !store.state.push), openRoute)

// Clicking a push's notification while a page is open here moves that
// page to the item.
navigator.serviceWorker?.addEventListener('message', (e: MessageEvent<{ type?: string; url?: string }>) => {
  if (e.data?.type !== 'open' || !e.data.url) return
  const route = parseInboxPath(new URL(e.data.url, location.origin).pathname)
  if (route) openRoute(route)
})

/** The phone's page: what's waiting, and the header's link to the citadel. */
function renderPhoneLayout(s: State): void {
  appEl.classList.toggle('phone', s.phone)
  phoneEl.hidden = !s.phone
  if (!s.phone) return
  inboxEl.hidden = true
  stage.hidden = true
  panelEl.hidden = true
  world?.setPaused(true)
  const n = pendingItems(s).length
  document.title = n ? `(${n}) Skylands` : 'Skylands'
  renderHeader(crumbsEl, connectedEl, linkEl, s, nav, versionEl)
  link.watch(null, null)
  terminals.show(null)
  renderPhone(phoneEl, s, s.route, phoneActions)
}

/**
 * Turns notifications on, asking the browser's permission first if it
 * hasn't been asked, or off. Where the citadel sends pushes, turning them
 * on subscribes this browser, and turning them off unsubscribes it.
 */
async function turnNotifications(on: boolean): Promise<void> {
  if (on && notifySupport() === 'ok' && Notification.permission === 'default') {
    try {
      await Notification.requestPermission()
    } catch {
      // A refusal or a dismissed prompt leaves the permission as it was.
    }
  }
  const granted = on && notifySupport() === 'ok' && Notification.permission === 'granted'
  store.setNotify(granted)
  if (granted) await syncPush()
  else if (pushSupport(store.state) !== 'unsupported') {
    pushedKey = ''
    store.update({ push: false, pushError: null })
    await unsubscribePush().catch(() => {})
  }
}

let pushedKey = ''

/**
 * Subscribes this browser to the citadel's pushes while its notifications
 * are on and the citadel offers them here, once for each push key. Doing
 * it on every page load registers the subscription again, which keeps it
 * current if the browser renewed it.
 */
async function syncPush(): Promise<void> {
  const s = store.state
  const key = s.world?.pushKey ?? ''
  if (!s.notify || !key || pushSupport(s) !== 'ok' || Notification.permission !== 'granted') {
    pushedKey = ''
    if (s.push) store.update({ push: false })
    return
  }
  if (pushedKey === key) return
  pushedKey = key
  try {
    await subscribePush(s)
    store.update({ push: true, pushError: null })
  } catch (err) {
    pushedKey = ''
    store.update({ push: false, pushError: (err as Error).message })
  }
}

let waitingKey: string | null = null

/** Closes the service worker's notifications for what no longer waits, each time that changes. */
function closeAnswered(s: State): void {
  if (s.link !== 'connected' || s.approvers === null || pushSupport(s) === 'unsupported') return
  const tags = waitingItems(s).map((w) => w.tag)
  const key = tags.sort().join(' ')
  if (key === waitingKey) return
  waitingKey = key
  void closeStale(new Set(tags)).catch(() => {})
}

function afterRender(s: State): void {
  notifier.observe(s)
  closeAnswered(s)
  void syncPush()
}

fitScreen()

store.subscribe((s0) => {
  newSession.update(s0)
  terminals.update(s0.sessions)
  renderPhoneLayout(s0)
  if (s0.phone) {
    afterRender(s0)
    return
  }
  // Reading a conversation in the inbox counts as having seen everything in
  // it so far. Following its airship in the world does not: no message is
  // shown there.
  const open = s0.view === 'inbox' && s0.inbox ? s0.sessions[s0.inbox] : undefined
  const s = open ? store.markViewed(open.id, Date.parse(open.updatedAt)) : s0
  const stale = isStale(s)
  renderMode(s)
  renderHeader(crumbsEl, connectedEl, linkEl, s, nav, versionEl)
  renderVeil(s)
  bar.update(s)
  if (s.view === 'inbox') renderInbox(inboxEl, s, cache, live, inboxActions, terminals)
  const reading = s.view === 'inbox' && s.inbox ? s.sessions[s.inbox] : undefined
  link.watch(reading?.host ?? null, reading?.id ?? null)
  terminals.show(terminalInSight(s, terminals))
  if (world) {
    for (const i of islandList(s)) {
      world.upsertIsland(i.host, i.reachable)
      world.setRepos(i.host, reposOn(s, i.host))
      world.setLabel(i.host, i.host, islandStatus(i, stale), 0)
      world.setAgents(
        i.host,
        Object.values(s.sessions)
          .filter((x) => x.host === i.host)
          .map(
            (x): AgentState => ({
              id: x.id,
              terrace: x.repoId,
              prov: providerOf(x.agent),
              name: agentLabel(x.agent),
              action: x.currentAction ?? '',
              status: x.status,
              waiting: isWaiting(s, x),
              approval: x.pending.length > 0 || x.questions.length > 0,
              file: x.currentFile ?? null,
              kind: x.currentFile && x.files[x.currentFile] === 'edit' ? 'edit' : 'read',
              atFile: !!x.recent?.length && !!x.recent[x.recent.length - 1]?.locations?.length,
              ctx: x.used && x.size ? (x.used / x.size) * 100 : 0,
            }),
          ),
      )
    }
    world.setLabel('cit', 'Citadel', citadelLabelStatus(s), 0)
    world.setBeacon(pendingCount(s) > 0)
    world.setStale(stale)
    world.setCitadelCrystal(!!s.server?.connected)
    world.setTheme(s.worldTheme)
  }
  renderPanel(s, stale)
  afterRender(s)
})

// A handle for debugging and screenshots: skylands.world.rig.flyTo(...).
;(window as unknown as { skylands: unknown }).skylands = {
  get world() {
    return world
  },
  store,
  renderMarkdown,
}

modeWorld.addEventListener('click', () => store.setView('world'))
modeInbox.addEventListener('click', () => store.setView('inbox'))

window.addEventListener('keydown', (e) => {
  const t = e.target as HTMLElement | null
  const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
  // Escape in a dialog, such as the New form, closes the dialog and no more.
  if (!typing && e.key === 'Escape' && !store.state.phone && !t?.closest('dialog')) world?.up()
})

// Times tick in place. Nothing is rebuilt for the clock. A running agent's
// diff, and its conversation in the inbox, are re-fetched every couple of
// seconds while open, which rebuilds only when they changed.
setInterval(() => {
  const s = store.state
  refreshTimes(panelEl)
  refreshTimes(inboxEl)
  refreshTimes(phoneEl)
  if (s.link === 'lost') renderVeil(s)
  const ag = s.view === 'world' && level(s) === 'agent' && s.agent ? s.sessions[s.agent] : undefined
  if (ag && ag.status === 'running') cache.get('diff', ag.host, ag.id, 2000)
  const open = s.view === 'inbox' && s.inbox ? s.sessions[s.inbox] : undefined
  if (open && open.status === 'running') cache.get('transcript', open.host, open.id, 15000)
  if (open) refreshOpenChips(open, cache)
}, 1000)

