import type { Island, Repo, ServerInfo } from './protocol'

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
}

export interface State extends Nav {
  link: LinkState
  /** The NATS server as the citadel last described it, or null before the first message. */
  server: ServerInfo | null
  /** Every island the citadel knows, by host. */
  islands: Record<string, Island>
  /** Every repository the citadel knows, by repo id. */
  repos: Record<string, Repo>
  /** When this page last heard from the citadel, in ms since the epoch, or null before the first contact. */
  lastContact: number | null
}

type Listener = (s: State) => void

/** One place for everything the page renders from. Every change notifies every listener. */
export class Store {
  state: State = { link: 'connecting', server: null, islands: {}, repos: {}, selected: 'cit', terrace: null, file: null, lastContact: null }
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

  /** Re-renders without changing anything, for times that tick. */
  touch(): void {
    this.notify()
  }

  private notify(): void {
    for (const fn of this.listeners) fn(this.state)
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

/** What the level of the current navigation is. */
export function level(n: Nav): 'world' | 'island' | 'terrace' | 'file' {
  if (n.file !== null) return 'file'
  if (n.terrace !== null) return 'terrace'
  if (n.selected !== 'cit') return 'island'
  return 'world'
}
