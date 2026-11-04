import type { Island, Repo, ServerInfo, Session, TokenUsage, WorldInfo } from './protocol'
import { isWorldTheme, type WorldThemeId } from './worldthemes'

/** The browser's link to its citadel over the WebSocket. */
export type LinkState = 'connecting' | 'connected' | 'lost'

/** Where the camera is: the world, an island, a terrace on it, or a file on that terrace. */
export interface Nav {
  /** 'cit' for the world level, or a host. */
  selected: string
  /** A repo id when landed on a terrace. */
  terrace: string | null
  /** A building index when looking at one file. */
  file: number | null
  /** A session id when following one agent. */
  agent: string | null
}

export interface State extends Nav {
  link: LinkState
  /** The NATS server as the citadel last described it, or null before the first message. */
  server: ServerInfo | null
  /** The world as the citadel last described it, or null before the first message. */
  world: WorldInfo | null
  /** Every island the citadel knows, by host. */
  islands: Record<string, Island>
  /** Every repository the citadel knows, by repo id. */
  repos: Record<string, Repo>
  /** Every agent session the citadel knows, by session id. */
  sessions: Record<string, Session>
  /** When this page last saw the citadel, in ms since the epoch, or null before the first contact. */
  lastContact: number | null
  /** When each session was last looked at here, in ms since the epoch. A per-viewer convenience. */
  viewed: Record<string, number>
  /** The world, or the inbox of conversations. */
  view: 'world' | 'inbox'
  /** The conversation open in the inbox, by session id. */
  inbox: string | null
  /** Which conversations the inbox lists: open ones, or all. */
  inboxFilter: 'open' | 'all'
  /** How the 3D world is drawn, as chosen in this browser. */
  worldTheme: WorldThemeId
}

const VIEWED_KEY = 'skylands.viewed'
const VIEW_KEY = 'skylands.view'
const FILTER_KEY = 'skylands.inboxFilter'
const WORLD_THEME_KEY = 'skylands.worldTheme'

function loadViewed(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(VIEWED_KEY) ?? '{}') as Record<string, number>
  } catch {
    return {}
  }
}

function loadFilter(): 'open' | 'all' {
  try {
    return localStorage.getItem(FILTER_KEY) === 'all' ? 'all' : 'open'
  } catch {
    return 'open'
  }
}

function loadView(): 'world' | 'inbox' {
  try {
    return localStorage.getItem(VIEW_KEY) === 'inbox' ? 'inbox' : 'world'
  } catch {
    return 'world'
  }
}

function loadWorldTheme(): WorldThemeId {
  try {
    const v = localStorage.getItem(WORLD_THEME_KEY)
    return isWorldTheme(v) ? v : 'default'
  } catch {
    return 'default'
  }
}

type Listener = (s: State) => void

/** One place for everything the page renders from. Every change notifies every listener. */
export class Store {
  state: State = { link: 'connecting', server: null, world: null, islands: {}, repos: {}, sessions: {}, selected: 'cit', terrace: null, file: null, agent: null, lastContact: null, viewed: loadViewed(), view: loadView(), inbox: null, inboxFilter: loadFilter(), worldTheme: loadWorldTheme() }
  private listeners: Listener[] = []

  subscribe(fn: Listener): void {
    this.listeners.push(fn)
    fn(this.state)
  }

  update(patch: Partial<State>): void {
    this.state = { ...this.state, ...patch }
    this.notify()
  }

  upsertIsland(island: Island): void {
    this.state = { ...this.state, islands: { ...this.state.islands, [island.host]: island } }
    this.notify()
  }

  upsertRepo(repo: Repo): void {
    this.state = { ...this.state, repos: { ...this.state.repos, [repo.id]: repo } }
    this.notify()
  }

  upsertSession(session: Session): void {
    this.state = { ...this.state, sessions: { ...this.state.sessions, [session.id]: session } }
    this.notify()
  }

  /**
   * Records that a session was looked at, up to the given time, and returns
   * the state. It does not notify: a render that shows the session calls it
   * on the way, so the mark is part of that render rather than a second one.
   */
  markViewed(id: string, at: number): State {
    if ((this.state.viewed[id] ?? 0) >= at) return this.state
    const viewed = { ...this.state.viewed, [id]: at }
    try {
      localStorage.setItem(VIEWED_KEY, JSON.stringify(viewed))
    } catch {
      // Browser storage can be unavailable. The mark then lasts the page.
    }
    this.state = { ...this.state, viewed }
    return this.state
  }

  /** Lists open conversations or all of them in the inbox, and remembers which. */
  setInboxFilter(inboxFilter: 'open' | 'all'): void {
    if (inboxFilter === this.state.inboxFilter) return
    try {
      localStorage.setItem(FILTER_KEY, inboxFilter)
    } catch {
      // Same as above.
    }
    this.update({ inboxFilter })
  }

  /** Switches between the world and the inbox, and remembers which. */
  setView(view: 'world' | 'inbox'): void {
    if (view === this.state.view) return
    try {
      localStorage.setItem(VIEW_KEY, view)
    } catch {
      // Same as above.
    }
    this.update({ view })
  }

  /** Draws the world in another theme, and remembers which. */
  setWorldTheme(worldTheme: WorldThemeId): void {
    if (worldTheme === this.state.worldTheme) return
    try {
      localStorage.setItem(WORLD_THEME_KEY, worldTheme)
    } catch {
      // Same as above.
    }
    this.update({ worldTheme })
  }

  /** Re-renders without changing anything, for times that tick. */
  touch(): void {
    this.notify()
  }

  private notify(): void {
    for (const fn of this.listeners) {
      try {
        fn(this.state)
      } catch (err) {
        // One broken render must not freeze the page or block the next update.
        console.error('render failed', err)
      }
    }
  }
}

/** Islands sorted by host, so lists are stable. */
export function islandList(s: State): Island[] {
  return Object.values(s.islands).sort((a, b) => a.host.localeCompare(b.host))
}

/** The repositories on one host, sorted by name. */
export function reposOn(s: State, host: string): Repo[] {
  return Object.values(s.repos)
    .filter((r) => r.host === host)
    .sort((a, b) => a.name.localeCompare(b.name))
}

/** The sessions on one repository, newest first. */
export function sessionsOn(s: State, repoId: string): Session[] {
  return Object.values(s.sessions)
    .filter((x) => x.repoId === repoId)
    .sort((a, b) => b.startedAt.localeCompare(a.startedAt))
}

/** What a set of sessions has cost, added up: a repository's spend from its sessions, or the world's from all of them. */
export function sumTokens(list: Session[]): TokenUsage {
  const sum = { input: 0, output: 0, cachedRead: 0, cachedWrite: 0, thought: 0, total: 0 }
  for (const { tokens: t } of list) {
    sum.input += t.input
    sum.output += t.output
    sum.cachedRead += t.cachedRead ?? 0
    sum.cachedWrite += t.cachedWrite ?? 0
    sum.thought += t.thought ?? 0
    sum.total += t.total
  }
  return sum
}

/** Whether the agent or a person did something in a session since it was last looked at here. Bookkeeping, such as a daemon restart, does not count. */
export function isUnread(s: State, x: Session): boolean {
  return Date.parse(x.activeAt) > (s.viewed[x.id] ?? 0)
}

/**
 * Whether a session waits on you: a tool call needs your approval, the
 * agent asked a question, or it stopped for a reply and you have not seen
 * its last message. Once seen, an idle session is merely idle; an approval
 * or a question waits until answered.
 */
export function isWaiting(s: State, x: Session): boolean {
  return x.pending.length > 0 || x.questions.length > 0 || (x.status === 'idle' && isUnread(s, x))
}

/** How many tool calls and questions wait on a person across the world, which lights the beacon. */
export function pendingCount(s: State): number {
  let n = 0
  for (const x of Object.values(s.sessions)) n += x.pending.length + x.questions.length
  return n
}

export function waitingCount(s: State): number {
  return Object.values(s.sessions).filter((x) => isWaiting(s, x)).length
}

/** Every session, most recently active first. */
export function conversationList(s: State): Session[] {
  return Object.values(s.sessions).sort((a, b) => b.activeAt.localeCompare(a.activeAt))
}

/** What the level of the current navigation is. */
export function level(n: Nav): 'world' | 'island' | 'terrace' | 'file' | 'agent' {
  if (n.agent !== null && n.terrace !== null) return 'agent'
  if (n.file !== null) return 'file'
  if (n.terrace !== null) return 'terrace'
  if (n.selected !== 'cit') return 'island'
  return 'world'
}
