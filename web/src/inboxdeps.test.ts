import { describe, expect, it, vi } from 'vitest'
import type { FetchCache } from './fetchcache'
import type { Session } from './protocol'
import type { State } from './store'

// The inbox's modules look at the window as they load.
vi.stubGlobal('window', {})
const { conversationDeps, listed } = await import('./inbox')

function session(id: string, over: Partial<Session> = {}): Session {
  return { id, host: 'workstation', repoId: 'r1', agent: 'claude', status: 'idle', pending: [], questions: [], terminals: [], startedAt: '2026-11-06T09:00:00Z', activeAt: '2026-11-06T09:00:00Z', updatedAt: '2026-11-06T09:00:00Z', ...over } as Session
}

const cache = { peek: () => undefined } as unknown as FetchCache
const terminals = { version: () => 0 }

// The store keeps the objects of whatever an update leaves alone.
const repos = { r1: { id: 'r1', name: 'repo' } }
const state = (sessions: Session[], viewed: Record<string, number> = {}): State =>
  ({ sessions: Object.fromEntries(sessions.map((x) => [x.id, x])), repos, viewed, islands: {}, convoMode: {}, approvers: null, me: undefined, world: null }) as unknown as State

const identical = (a: unknown[], b: unknown[]): boolean => a.length === b.length && a.every((d, i) => d === b[i])

describe('conversationDeps', () => {
  const open = session('open')
  const busy = session('busy', { status: 'running' })
  const before = state([open, busy])
  const deps = conversationDeps(before, open, undefined, undefined, cache, terminals)

  it('stays the same while other sessions work', () => {
    const after = state([open, { ...busy, activeAt: '2026-11-06T09:05:00Z', updatedAt: '2026-11-06T09:05:00Z' }], { busy: Date.parse('2026-11-06T09:05:00Z') })
    expect(identical(conversationDeps(after, after.sessions.open, undefined, undefined, cache, terminals), deps)).toBe(true)
  })

  it('moves on when the open session changes', () => {
    const moved = { ...open, status: 'running' as const }
    const after = state([moved, busy])
    expect(identical(conversationDeps(after, moved, undefined, undefined, cache, terminals), deps)).toBe(false)
  })
})

describe('listed', () => {
  const idle = session('idle')
  const ended = session('ended', { status: 'ended' })
  const failed = session('failed', { status: 'failed' })
  const ids = (s: State): string[] => listed(s).map((x) => x.id).sort()
  const under = (inboxFilter: 'open' | 'all', inbox: string | null): State => ({ ...state([idle, ended, failed]), inboxFilter, inbox })

  it('leaves a finished conversation out of Open even while it is shown', () => {
    expect(ids(under('open', 'ended'))).toEqual(['idle'])
    expect(ids(under('open', null))).toEqual(['idle'])
  })

  it('lists every conversation under All', () => {
    expect(ids(under('all', 'ended'))).toEqual(['ended', 'failed', 'idle'])
  })
})
