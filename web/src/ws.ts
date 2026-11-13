import type { Island, Live, Message, Repo, Session, TerminalOutput, WorldInfo } from './protocol'
import { signedOut, signIn } from './signin'
import type { State, Store } from './store'

/** The browser's side of the socket, beyond listening. */
export interface Link {
  /** Reads one session's transcript as it is written, or none. */
  watch(host: string | null, id: string | null): void
  /** Follows what one terminal's shell writes, or none. */
  watchTerminal(t: TerminalRef | null): void
  /** Sends what a person typed, signed for the daemon to check. False when the socket is down and it wasn't sent. */
  terminalInput(t: TerminalRef, input: { attach: string; seq: number; data: string; signature?: unknown }): boolean
  /** Tells a terminal its view's new size. */
  terminalSize(t: TerminalRef, cols: number, rows: number): void
}

/** One terminal: its island, its session, and its id. */
export interface TerminalRef {
  host: string
  id: string
  terminal: string
}

/** What the page does with its terminals' traffic. */
export interface TerminalListener {
  output(t: TerminalRef, out: TerminalOutput): void
  /** The socket came back, so what was written meanwhile is missing. */
  reconnected(): void
}

/** Fills in lists an older citadel might send as null, so renderers never check. */
function normalizeSession(s: Session): Session {
  return { ...s, recent: s.recent ?? [], files: s.files ?? {}, plan: s.plan ?? [], pending: s.pending ?? [], questions: s.questions ?? [], rules: s.rules ?? [], terminals: s.terminals ?? [], trusted: s.trusted ?? false, turns: s.turns ?? 1, updatedAt: s.updatedAt ?? s.startedAt, activeAt: s.activeAt ?? s.updatedAt ?? s.startedAt }
}

/**
 * What a snapshot changes. A citadel still catching up, as one does when it
 * has just restarted, sends only the hosts it has read so far. A page that
 * already shows the world keeps showing it until the citadel has caught up
 * and sends the whole of it, so nothing on the page moves meanwhile. A page
 * with nothing yet shows what there is.
 */
export function snapshotPatch(m: Message, shown: boolean): Partial<State> {
  const loading = m.loading ?? null
  const patch: Partial<State> = { server: m.server ?? null, world: m.world ?? null, approvers: m.approvers ?? [], themes: m.themes ?? [], loading }
  if (loading && shown) return patch
  const islands: Record<string, Island> = {}
  for (const i of m.islands ?? []) islands[i.host] = i
  const repos: Record<string, Repo> = {}
  for (const r of m.repos ?? []) repos[r.id] = r
  const sessions: Record<string, Session> = {}
  for (const x of m.sessions ?? []) sessions[x.id] = normalizeSession(x)
  return { ...patch, islands, repos, sessions }
}

// The browser stays on one citadel through this socket. When it drops, keep
// trying with a growing delay so a citadel restart heals on its own.
export function connectToCitadel(store: Store, onLive: (host: string, id: string, piece: Live) => void, terms?: TerminalListener): Link {
  let retryDelay = 1000
  let ws: WebSocket | null = null
  let watched: { host: string; id: string } | null = null
  let term: TerminalRef | null = null
  let opened = 0
  // Whether a citadel has sent this page the whole world, which the page
  // keeps showing through a citadel's catching up.
  let shown = false

  const send = (m: unknown): boolean => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return false
    ws.send(JSON.stringify(m))
    return true
  }
  const tell = (): void => {
    send(watched ? { type: 'watch', ...watched } : { type: 'unwatch' })
  }
  const tellTerm = (): void => {
    send(term ? { type: 'term-watch', ...term } : { type: 'term-unwatch' })
  }

  // Whether the page is in sight, which holds the citadel's pushes to this
  // browser for a moment, in case they're answered here.
  const tellSight = (): void => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify({ type: document.visibilityState === 'visible' ? 'visible' : 'hidden' }))
  }
  document.addEventListener('visibilitychange', tellSight)

  const retry = (): void => {
    store.update({ link: 'lost' })
    setTimeout(() => void connect(), retryDelay)
    retryDelay = Math.min(retryDelay * 2, 10000)
  }

  // Every connection signs in first, when the page opens and on each
  // reconnect, which is also how a browser whose approver was revoked, and
  // whose socket the citadel closed, finds out.
  const connect = async (): Promise<void> => {
    const r = await signIn()
    if (r === 'no-key' || r === 'refused') return signedOut()
    if (r === 'unavailable') return retry()
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${scheme}://${location.host}/ws`)
    // The link counts as connected once the citadel's snapshot says where
    // it stands, which it sends first.
    ws.addEventListener('open', () => {
      retryDelay = 1000
      // A reconnection picks the watch back up.
      if (watched) tell()
      if (term) tellTerm()
      if (opened++ > 0) terms?.reconnected()
      tellSight()
    })
    ws.addEventListener('message', (ev: MessageEvent<string>) => {
      const m = JSON.parse(ev.data) as Message
      store.state.lastContact = Date.now()
      switch (m.type) {
        case 'live':
          if (m.host && m.id && m.live && watched && m.host === watched.host && m.id === watched.id) onLive(m.host, m.id, m.live)
          break
        case 'term':
          if (m.host && m.id && m.terminal && m.output && term && m.host === term.host && m.id === term.id && m.terminal === term.terminal) terms?.output(term, m.output)
          break
        case 'snapshot':
          store.update({ ...snapshotPatch(m, shown), link: 'connected' })
          if (!m.loading) shown = true
          if (needsSetup(m.world)) location.assign('/setup')
          break
        case 'loading':
          store.update({ loading: m.loading ?? null })
          break
        case 'approvers':
          store.update({ approvers: m.approvers ?? [] })
          break
        case 'themes':
          store.update({ themes: m.themes ?? [] })
          break
        case 'world':
          if (m.world) store.update({ world: m.world })
          if (needsSetup(m.world)) location.assign('/setup')
          break
        case 'server':
          if (m.server) store.update({ server: m.server })
          break
        case 'island':
          if (m.island) store.upsertIsland(m.island)
          break
        case 'repo':
          if (m.repo) store.upsertRepo(m.repo)
          break
        case 'session':
          if (m.session) store.upsertSession(normalizeSession(m.session))
          break
      }
    })
    ws.addEventListener('close', retry)
  }

  void connect()
  return {
    watch(host, id) {
      const next = host && id ? { host, id } : null
      if (next?.host === watched?.host && next?.id === watched?.id) return
      watched = next
      tell()
    },
    watchTerminal(t) {
      if (t?.host === term?.host && t?.id === term?.id && t?.terminal === term?.terminal) return
      term = t ? { ...t } : null
      tellTerm()
    },
    terminalInput(t, input) {
      return send({ type: 'term-input', ...t, input })
    },
    terminalSize(t, cols, rows) {
      send({ type: 'term-size', ...t, size: { cols, rows } })
    },
  }
}

/** A world the citadel has looked for and not found: the page to show is setup. */
function needsSetup(w: WorldInfo | undefined): boolean {
  return !!w && w.checked && !w.ready
}
