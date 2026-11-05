import { keyFromSeed, loadApprover, sign } from './approver'

/**
 * Signing in to the citadel with this browser's approver key. The citadel
 * names what to sign, from its own clock; the signed sign-in comes back as
 * an HTTP-only cookie that any citadel of the world checks against the
 * approvers, so no citadel keeps a session. The page signs in whenever it
 * opens the world or reconnects, and again when a request says the sign-in
 * lapsed, so a person never does it by hand.
 *
 * - signed-in: the cookie is set.
 * - open: the world isn't set up yet, so there's no approver to sign in as
 *   and nothing asks for one.
 * - no-key: this browser has no approver key for this world.
 * - refused: its key no longer approves for this world.
 * - unavailable: the citadel can't be reached, or can't tell yet; try again.
 */
export type SignInResult = 'signed-in' | 'open' | 'no-key' | 'refused' | 'unavailable'

/** What GET /api/signin offers to sign. */
interface Challenge {
  world: string
  citadel: string
  issued: string
  expires: string
}

let inFlight: Promise<SignInResult> | null = null

/** Signs in, sharing one attempt among everything that asks at once. */
export function signIn(): Promise<SignInResult> {
  inFlight ??= attempt().finally(() => {
    inFlight = null
  })
  return inFlight
}

async function attempt(): Promise<SignInResult> {
  let resp: Response
  try {
    resp = await fetch('/api/signin', { cache: 'no-store' })
  } catch {
    return 'unavailable'
  }
  if (resp.status === 409) return 'open'
  if (!resp.ok) return 'unavailable'
  const c = (await resp.json().catch(() => null)) as Challenge | null
  if (!c || typeof c.world !== 'string' || typeof c.citadel !== 'string' || typeof c.issued !== 'string' || typeof c.expires !== 'string') return 'unavailable'
  const me = await loadApprover().catch(() => null)
  if (!me || me.world !== c.world) return 'no-key'
  const signin = sign(keyFromSeed(me.seed), { type: 'signin', world: c.world, citadel: c.citadel, issued: c.issued, expires: c.expires })
  try {
    resp = await fetch('/api/signin', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ signin }) })
  } catch {
    return 'unavailable'
  }
  if (resp.ok) return 'signed-in'
  return resp.status === 403 ? 'refused' : 'unavailable'
}

/** A browser that can't sign in goes to the setup page, which says why and offers an invite link or the recovery file. */
export function signedOut(): void {
  location.replace('/setup')
}

/**
 * fetch for the citadel's API. When the answer says the sign-in lapsed, it
 * signs in again and asks once more, and sends a browser that can't sign in
 * to the setup page. A body to send again must be a string.
 */
export async function api(input: string, init?: RequestInit): Promise<Response> {
  const resp = await fetch(input, init)
  if (resp.status !== 401) return resp
  const r = await signIn()
  if (r === 'no-key' || r === 'refused') {
    signedOut()
    return resp
  }
  return r === 'signed-in' ? fetch(input, init) : resp
}
