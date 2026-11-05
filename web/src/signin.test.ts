import { createUser, fromPublic } from '@nats-io/nkeys'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { StoredApprover } from './approver'

// This browser's stored key, which each test sets.
let stored: StoredApprover | null = null
vi.mock('./approver', async (orig) => ({ ...(await orig<typeof import('./approver')>()), loadApprover: async () => stored }))

const { api, signIn } = await import('./signin')

const kp = createUser()
const challenge = { world: 'OWORLD', citadel: '127.0.0.1:8080', issued: '2026-11-05T16:00:00Z', expires: '2026-11-06T04:00:00Z' }
const json = (status: number, body: unknown): Response => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

/** Answers fetch from a list of handlers, one per call, and records the calls. */
function serve(...answers: ((init?: RequestInit) => Response)[]) {
  const calls: { url: string; init?: RequestInit }[] = []
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    calls.push({ url, init })
    const next = answers.shift()
    if (!next) throw new Error(`unexpected fetch ${url}`)
    return next(init)
  })
  return calls
}

describe('signIn', () => {
  let replaced: string | null
  beforeEach(() => {
    stored = { seed: new TextDecoder().decode(kp.getSeed()), key: kp.getPublicKey(), name: 'Chrome on Linux', world: 'OWORLD', created: '2026-11-05T15:00:00Z' }
    replaced = null
    vi.stubGlobal('location', { replace: (to: string) => (replaced = to) })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('signs exactly what the citadel offered, with this browser’s key', async () => {
    const calls = serve(
      () => json(200, challenge),
      () => json(200, { key: kp.getPublicKey() }),
    )
    expect(await signIn()).toBe('signed-in')
    const sent = JSON.parse(calls[1].init?.body as string) as { signin: { payload: string; signer: string; sig: string } }
    expect(calls[1].url).toBe('/api/signin')
    expect(JSON.parse(sent.signin.payload)).toEqual({ type: 'signin', ...challenge })
    expect(sent.signin.signer).toBe(kp.getPublicKey())
    const sig = Uint8Array.from(atob(sent.signin.sig.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0))
    expect(fromPublic(sent.signin.signer).verify(new TextEncoder().encode(sent.signin.payload), sig)).toBe(true)
  })

  it('needs no sign-in before the world exists', async () => {
    serve(() => json(409, { error: 'this world isn’t set up yet' }))
    expect(await signIn()).toBe('open')
  })

  it('says when this browser has no key for this world', async () => {
    serve(() => json(200, challenge))
    stored = null
    expect(await signIn()).toBe('no-key')
    serve(() => json(200, { ...challenge, world: 'OANOTHER' }))
    stored = { ...stored!, world: 'OWORLD' }
    expect(await signIn()).toBe('no-key')
  })

  it('says when the key no longer approves, and when to try again', async () => {
    serve(
      () => json(200, challenge),
      () => json(403, { error: 'this browser doesn’t approve for this world' }),
    )
    expect(await signIn()).toBe('refused')
    serve(() => json(503, { error: 'the citadel hasn’t read the approvers yet' }))
    expect(await signIn()).toBe('unavailable')
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('Failed to fetch')
    })
    expect(await signIn()).toBe('unavailable')
  })

  it('shares one attempt among callers at once', async () => {
    const calls = serve(
      () => json(200, challenge),
      () => json(200, {}),
    )
    expect(await Promise.all([signIn(), signIn(), signIn()])).toEqual(['signed-in', 'signed-in', 'signed-in'])
    expect(calls).toHaveLength(2)
  })

  it('signs in again once when a call says the sign-in lapsed', async () => {
    const calls = serve(
      () => json(401, { error: 'sign in' }),
      () => json(200, challenge),
      () => json(200, {}),
      (init) => json(200, { echoed: init?.body }),
    )
    const resp = await api('/api/sessions', { method: 'POST', body: '{"prompt":"hi"}' })
    expect(resp.status).toBe(200)
    expect(await resp.json()).toEqual({ echoed: '{"prompt":"hi"}' })
    expect(calls.map((c) => c.url)).toEqual(['/api/sessions', '/api/signin', '/api/signin', '/api/sessions'])
    expect(replaced).toBeNull()
  })

  it('sends a browser that can’t sign in to the setup page', async () => {
    serve(
      () => json(401, { error: 'sign in' }),
      () => json(200, challenge),
      () => json(403, { error: 'no longer approves' }),
    )
    const resp = await api('/api/sessions/h/x/transcript')
    expect(resp.status).toBe(401)
    expect(replaced).toBe('/setup')
  })
})
