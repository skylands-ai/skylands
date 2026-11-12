import { describe, expect, it } from 'vitest'
import type { Island, Message, Session } from './protocol'
import { snapshotPatch } from './ws'

const island = { host: 'ws', reachable: true } as Island
const session = { id: 's1', host: 'ws', repoId: 'r1', startedAt: '2026-11-12T10:00:00Z' } as Session

describe('snapshotPatch', () => {
  it('takes the whole world from a citadel that has caught up', () => {
    const p = snapshotPatch({ type: 'snapshot', islands: [island], sessions: [session] }, true)
    expect(p.loading).toBeNull()
    expect(Object.keys(p.islands ?? {})).toEqual(['ws'])
    expect(p.sessions?.s1?.turns).toBe(1)
    expect(p.sessions?.s1?.pending).toEqual([])
  })

  it('keeps what a page shows while the citadel catches up, and takes the rest', () => {
    const m: Message = { type: 'snapshot', loading: { hosts: ['ws'] }, server: { connected: true }, approvers: [] }
    const p = snapshotPatch(m, true)
    expect(p.loading).toEqual({ hosts: ['ws'] })
    expect(p.server).toEqual({ connected: true })
    expect('islands' in p || 'repos' in p || 'sessions' in p).toBe(false)
  })

  it('shows what there is on a page that has had nothing yet', () => {
    const p = snapshotPatch({ type: 'snapshot', loading: { nats: true } }, false)
    expect(p.loading).toEqual({ nats: true })
    expect(p.islands).toEqual({})
    expect(p.sessions).toEqual({})
  })
})
