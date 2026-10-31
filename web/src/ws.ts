import type { Island, Message, Repo, Session } from './protocol'
import type { Store } from './store'

/** Fills in lists an older citadel might send as null, so renderers never check. */
function normalizeSession(s: Session): Session {
  return { ...s, recent: s.recent ?? [], files: s.files ?? {}, plan: s.plan ?? [], turns: s.turns ?? 1, updatedAt: s.updatedAt ?? s.startedAt }
}

// The browser stays on one citadel through this socket. When it drops, keep
// trying with a growing delay so a citadel restart heals on its own.
export function connectToCitadel(store: Store): void {
  let retryDelay = 1000

  const connect = (): void => {
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${scheme}://${location.host}/ws`)
    ws.addEventListener('open', () => {
      retryDelay = 1000
      store.update({ link: 'connected', lastContact: Date.now() })
    })
    ws.addEventListener('message', (ev: MessageEvent<string>) => {
      const m = JSON.parse(ev.data) as Message
      store.state.lastContact = Date.now()
      switch (m.type) {
        case 'snapshot': {
          const islands: Record<string, Island> = {}
          for (const i of m.islands ?? []) islands[i.host] = i
          const repos: Record<string, Repo> = {}
          for (const r of m.repos ?? []) repos[r.id] = r
          const sessions: Record<string, Session> = {}
          for (const x of m.sessions ?? []) sessions[x.id] = normalizeSession(x)
          store.update({ server: m.server ?? null, islands, repos, sessions })
          break
        }
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
}
