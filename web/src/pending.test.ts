import { describe, expect, it } from 'vitest'
import type { Permission, Question, Session } from './protocol'
import { pendingItems, type State } from './store'

function session(id: string, over: Partial<Session> = {}): Session {
  return { id, host: 'workstation', repoId: 'r1', agent: 'claude', status: 'running', pending: [], questions: [], ...over } as Session
}

const request = (id: string, at: string): Permission => ({ requestId: id, toolCallId: 't', title: `Run ${id}`, inputHash: 'h', inputSize: 1, options: [], at })
const question = (id: string, at: string): Question => ({ questionId: id, message: `Ask ${id}`, fields: [], at })

describe('pendingItems', () => {
  const s = {
    sessions: {
      s1: session('s1', { pending: [request('r1', '2026-11-06T10:00:00Z')], questions: [question('q1', '2026-11-06T10:05:00Z')] }),
      s2: session('s2', { pending: [request('r2', '2026-11-06T10:02:00Z')] }),
      // An agent waiting for a reply is no tool call or question.
      s3: session('s3', { status: 'idle', activeAt: '2026-11-06T10:09:00Z' }),
    },
  } as unknown as State

  it('lists tool calls and questions across sessions, newest first', () => {
    expect(pendingItems(s).map((i) => [i.kind, i.id, i.x.id, i.title])).toEqual([
      ['question', 'q1', 's1', 'Ask q1'],
      ['request', 'r2', 's2', 'Run r2'],
      ['request', 'r1', 's1', 'Run r1'],
    ])
  })

  it('lists one session’s alone', () => {
    expect(pendingItems(s, 's1').map((i) => i.id)).toEqual(['q1', 'r1'])
    expect(pendingItems(s, 's3')).toEqual([])
  })
})
