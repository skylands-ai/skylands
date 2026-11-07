import { describe, expect, it } from 'vitest'
import { inboxPath, parseInboxPath } from './route'

describe('parseInboxPath', () => {
  it('reads the list, a session, a request, and a question', () => {
    expect(parseInboxPath('/inbox')).toEqual({})
    expect(parseInboxPath('/inbox/')).toEqual({})
    expect(parseInboxPath('/inbox/workstation/S1')).toEqual({ host: 'workstation', session: 'S1' })
    expect(parseInboxPath('/inbox/workstation/S1/requests/R1')).toEqual({ host: 'workstation', session: 'S1', item: { kind: 'request', id: 'R1' } })
    expect(parseInboxPath('/inbox/workstation/S1/questions/Q1')).toEqual({ host: 'workstation', session: 'S1', item: { kind: 'question', id: 'Q1' } })
  })

  it('takes any other path under /inbox as the list, and leaves the rest alone', () => {
    for (const path of ['/inbox/workstation', '/inbox/workstation/S1/answers/A1', '/inbox/a/b/requests/c/d', '/inbox/%E0%A4%A/S1']) expect(parseInboxPath(path)).toEqual({})
    for (const path of ['/', '/setup', '/inboxes', '/index.html']) expect(parseInboxPath(path)).toBeNull()
  })

  it('round-trips names that need escaping', () => {
    const route = { host: 'my box', session: 'S/1', item: { kind: 'request' as const, id: 'R?1' } }
    expect(inboxPath(route)).toBe('/inbox/my%20box/S%2F1/requests/R%3F1')
    expect(parseInboxPath(inboxPath(route))).toEqual(route)
    expect(inboxPath({})).toBe('/inbox')
    expect(inboxPath({ host: 'h', session: 's' })).toBe('/inbox/h/s')
  })
})
