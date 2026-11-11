import { describe, expect, it } from 'vitest'
import type { Session } from './protocol'
import { leftBehind } from './status'

function session(id: string, over: Partial<Session> = {}): Session {
  return { id, host: 'workstation', repoId: 'r1', agent: 'claude', status: 'ended', branch: 'skylands/' + id, pending: [], questions: [], ...over } as Session
}

describe('leftBehind', () => {
  it('names ended and failed sessions that still have a worktree of their own', () => {
    const sessions = [
      session('ended'),
      session('failed', { status: 'failed' }),
      session('running', { status: 'running' }),
      session('idle', { status: 'idle' }),
      session('removed', { worktreeRemoved: true }),
      session('checkout', { inCheckout: true }),
    ]
    expect(leftBehind(sessions).map((s) => s.id)).toEqual(['ended', 'failed'])
  })
})
