import type { Island, Live, Message, Repo, Session, WorldInfo } from './protocol'
import type { Store } from './store'

/** The browser's side of the socket, beyond listening. */
export interface Link {
  /** Reads one session's transcript as it is written, or none. */
  watch(host: string | null, id: string | null): void
}

/** Fills in lists an older citadel might send as null, so renderers never check. */
function normalizeSession(s: Session): Session {
  return { ...s, recent: s.recent ?? [], files: s.files ?? {}, plan: s.plan ?? [], pending: s.pending ?? [], questions: s.questions ?? [], rules: s.rules ?? [], trusted: s.trusted ?? false, turns: s.turns ?? 1, updatedAt: s.updatedAt ?? s.startedAt, activeAt: s.activeAt ?? s.updatedAt ?? s.startedAt }
}

// The browser stays on one citadel through this socket. When it drops, keep
// trying with a growing delay so a citadel restart heals on its own.
export function connectToCitadel(store: Store, onLive: (host: string, id: string, piece: Live) => void): Link {
  let retryDelay = 1000
  let ws: WebSocket | null = null
  let watched: { host: string; id: string } | null = null

  const tell = (): void => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(JSON.stringify(watched ? { type: 'watch', ...watched } : { type: 'unwatch' }))
  }

  const connect = (): void => {
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${scheme}://${location.host}/ws`)
    ws.addEventListener('open', () => {
      retryDelay = 1000
      store.update({ link: 'connected', lastContact: Date.now() })
      // A reconnection picks the watch back up.
      if (watched) tell()
    })
    ws.addEventListener('message', (ev: MessageEvent<string>) => {
      const m = JSON.parse(ev.data) as Message
      store.state.lastContact = Date.now()
      switch (m.type) {
        case 'live':
          if (m.host && m.id && m.live && watched && m.host === watched.host && m.id === watched.id) onLive(m.host, m.id, m.live)
          break
        case 'snapshot': {
          const islands: Record<string, Island> = {}
          for (const i of m.islands ?? []) islands[i.host] = i
          const repos: Record<string, Repo> = {}
          for (const r of m.repos ?? []) repos[r.id] = r
          const sessions: Record<string, Session> = {}
          for (const x of m.sessions ?? []) sessions[x.id] = normalizeSession(x)
          store.update({ server: m.server ?? null, world: m.world ?? null, islands, repos, sessions })
          if (needsSetup(m.world)) location.assign('/setup')
          break
        }
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
    ws.addEventListener('close', () => {
      store.update({ link: 'lost' })
      setTimeout(connect, retryDelay)
      retryDelay = Math.min(retryDelay * 2, 10000)
    })
  }

  connect()
  return {
    watch(host, id) {
      const next = host && id ? { host, id } : null
      if (next?.host === watched?.host && next?.id === watched?.id) return
      watched = next
      tell()
    },
  }
}

/** A world the citadel has looked for and not found: the page to show is setup. */
function needsSetup(w: WorldInfo | undefined): boolean {
  return !!w && w.checked && !w.ready
}
