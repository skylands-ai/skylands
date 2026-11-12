import { describe, expect, it } from 'vitest'
import type { Session } from './protocol'
import { conversationList, type State } from './store'

function session(id: string, startedAt: string, activeAt: string): Session {
  return { id, host: 'workstation', repoId: 'r1', agent: 'claude', status: 'running', pending: [], questions: [], startedAt, activeAt } as unknown as Session
}

const world = (...xs: Session[]): State => ({ sessions: Object.fromEntries(xs.map((x) => [x.id, x])) }) as unknown as State

describe('conversationList', () => {
  it('lists the newest session first, whatever was active last', () => {
    const s = world(
      session('old', '2026-11-06T09:00:00Z', '2026-11-06T10:30:00Z'),
      session('new', '2026-11-06T10:00:00Z', '2026-11-06T10:01:00Z'),
      session('mid', '2026-11-06T09:30:00Z', '2026-11-06T10:20:00Z'),
    )
    expect(conversationList(s).map((x) => x.id)).toEqual(['new', 'mid', 'old'])
  })

  it('keeps its order as agents work', () => {
    const a = session('a', '2026-11-06T09:00:00Z', '2026-11-06T09:00:00Z')
    const b = session('b', '2026-11-06T09:05:00Z', '2026-11-06T09:05:00Z')
    const before = conversationList(world(a, b)).map((x) => x.id)
    const after = conversationList(world({ ...a, activeAt: '2026-11-06T11:00:00Z' }, b)).map((x) => x.id)
    expect(after).toEqual(before)
  })

  it('compares times, not their text', () => {
    // Go leaves off a fraction of zero, and "Z" sorts after ".", so as text
    // the earlier time would come out the later.
    const s = world(session('whole', '2026-11-06T10:00:00Z', ''), session('fraction', '2026-11-06T10:00:00.5Z', ''))
    expect(conversationList(s).map((x) => x.id)).toEqual(['fraction', 'whole'])
  })

  it('orders sessions that started together by id, however they arrived', () => {
    const at = '2026-11-06T10:00:00Z'
    const ids = (xs: Session[]): string[] => conversationList(world(...xs)).map((x) => x.id)
    expect(ids([session('b', at, ''), session('a', at, '')])).toEqual(['a', 'b'])
    expect(ids([session('a', at, ''), session('b', at, '')])).toEqual(['a', 'b'])
  })
})
