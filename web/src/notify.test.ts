import { describe, expect, it } from 'vitest'
import { Notifier, waitingItems, type Notices, type Waiting } from './notify'
import type { Permission, Question, Session } from './protocol'
import type { State } from './store'

function session(id: string, over: Partial<Session> = {}): Session {
  return { id, host: 'workstation', repoId: 'r1', agent: 'claude', status: 'running', pending: [], questions: [], ...over } as Session
}

function request(id: string, kind?: string): Permission {
  return { requestId: id, toolCallId: 't-' + id, title: 'Run cargo test -p storefront-pricing', kind, inputHash: 'h', inputSize: 1, inputPreview: 'cargo test -p storefront-pricing', options: [], at: '' }
}

function question(id: string): Question {
  return { questionId: id, message: 'Which branch has the secret fix?', fields: [], at: '' }
}

function world(sessions: Session[], loaded = true): State {
  return {
    approvers: loaded ? [] : null,
    repos: { r1: { id: 'r1', host: 'workstation', name: 'storefront', path: '/src/storefront' } },
    sessions: Object.fromEntries(sessions.map((x) => [x.id, x])),
    viewed: {},
  } as unknown as State
}

/** Notices that record what they show and close. */
class Recorder implements Notices {
  on = true
  away = true
  open: string[] = []
  clicks = new Map<string, () => void>()
  allowed = (): boolean => this.on
  hidden = (): boolean => this.away
  show(w: Waiting, onClick: () => void) {
    this.open.push(w.tag)
    this.clicks.set(w.tag, onClick)
    return { close: () => (this.open = this.open.filter((t) => t !== w.tag)) }
  }
}

describe('waitingItems', () => {
  it('names the agent, repository, and island, and what it wants, but never the input', () => {
    const items = waitingItems(world([session('s1', { pending: [request('q1', 'execute'), request('q2', 'edit'), request('q3')], questions: [question('a1')] })]))
    expect(items.map((w) => [w.tag, w.title])).toEqual([
      ['request:q1', 'Claude Code in storefront on workstation wants to run a command'],
      ['request:q2', 'Claude Code in storefront on workstation wants to edit a file'],
      ['request:q3', 'Claude Code in storefront on workstation wants your approval'],
      ['question:a1', 'Claude Code in storefront on workstation has a question'],
    ])
    const text = JSON.stringify(items)
    expect(text).not.toContain('cargo')
    expect(text).not.toContain('secret')
  })

  it('leaves out an agent that stopped for a reply', () => {
    expect(waitingItems(world([session('s1', { status: 'idle', activeAt: new Date().toISOString() })]))).toEqual([])
  })
})

describe('Notifier', () => {
  it('notifies only what starts waiting after the page has loaded, while the page is out of sight', () => {
    const r = new Recorder()
    const n = new Notifier(r, () => {})
    // Nothing from the citadel yet, then the first snapshot: what already waited stays quiet.
    n.observe(world([session('s1', { pending: [request('old')] })], false))
    n.observe(world([session('s1', { pending: [request('old')] })]))
    expect(r.open).toEqual([])
    // A new request while the page is in the background notifies.
    n.observe(world([session('s1', { pending: [request('old'), request('new', 'execute')] })]))
    expect(r.open).toEqual(['request:new'])
    // One that arrives while the page is in sight doesn't, then or later.
    r.away = false
    n.observe(world([session('s1', { pending: [request('old'), request('new'), request('seen')] })]))
    r.away = true
    n.observe(world([session('s1', { pending: [request('old'), request('new'), request('seen')] })]))
    expect(r.open).toEqual(['request:new'])
  })

  it('closes a notification once what it names is answered, anywhere', () => {
    const r = new Recorder()
    const n = new Notifier(r, () => {})
    n.observe(world([]))
    n.observe(world([session('s1', { pending: [request('q1')], questions: [question('a1')] })]))
    expect(r.open).toEqual(['request:q1', 'question:a1'])
    n.observe(world([session('s1', { questions: [question('a1')] })]))
    expect(r.open).toEqual(['question:a1'])
    n.observe(world([session('s1')]))
    expect(r.open).toEqual([])
  })

  it('opens the conversation when clicked, and closes the notification', () => {
    const r = new Recorder()
    const opened: string[] = []
    const n = new Notifier(r, (id) => opened.push(id))
    n.observe(world([]))
    n.observe(world([session('s1', { pending: [request('q1')] })]))
    r.clicks.get('request:q1')!()
    expect(opened).toEqual(['s1'])
    expect(r.open).toEqual([])
  })

  it('shows nothing while turned off, and closes what it showed when turned off', () => {
    const r = new Recorder()
    const n = new Notifier(r, () => {})
    n.observe(world([]))
    n.observe(world([session('s1', { pending: [request('q1')] })]))
    r.on = false
    n.observe(world([session('s1', { pending: [request('q1'), request('q2')] })]))
    expect(r.open).toEqual([])
    // Turned back on, it notifies only what is new from then.
    r.on = true
    n.observe(world([session('s1', { pending: [request('q1'), request('q2'), request('q3')] })]))
    expect(r.open).toEqual(['request:q3'])
  })
})
