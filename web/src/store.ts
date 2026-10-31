import type { Island, ServerInfo } from './protocol'

/** The browser's link to its citadel over the WebSocket. */
export type LinkState = 'connecting' | 'connected' | 'lost'

export interface State {
  link: LinkState
  /** The NATS server as the citadel last described it, or null before the first message. */
  server: ServerInfo | null
  /** Every island the citadel knows, by host. */
  islands: Record<string, Island>
  /** What the world has selected: 'cit' or a host. */
  selected: string
  /** When this page last heard from the citadel, in ms since the epoch, or null before the first contact. */
  lastContact: number | null
}

type Listener = (s: State) => void

/** One place for everything the page renders from. Every change notifies every listener. */
export class Store {
  state: State = { link: 'connecting', server: null, islands: {}, selected: 'cit', lastContact: null }
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
