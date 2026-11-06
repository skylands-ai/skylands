import { keepStorage, keyFromSeed, loadApprover, newApproverKey, seedOf, sign, type Signed } from './approver'
import { citadelAddress, linkAt } from './address'
import { answersHash, DECISION_VERSION, formHash, normalizeRules, rulesHash, signDecision, textHash, type Decision, type DecisionSignature } from './decision'
import type { ApproverInfo, Permission, Question, Repo, Rule, Session, VoucherInfo } from './protocol'
import { api } from './signin'
import type { State, Store } from './store'

/** What the Approvers section can do: show an "Add an approver" link, and revoke an approver once confirmed. */
export interface ApproverActions {
  invite(): void
  /** Makes the link open the world at another address; the claim stays the same. */
  inviteAt(address: string): void
  /** Starts or stops typing another address for the link, or says what's wrong with it. */
  inviteOther(other: { text: string; error: string } | null): void
  closeInvite(): void
  /** Asks for a confirmation before revoking key, or stops asking with null. */
  askRevoke(key: string | null): void
  revoke(key: string): void
}

/** Reads this browser's approver key, and whether its storage is kept, into the store. */
export async function loadThisBrowser(store: Store): Promise<void> {
  const me = await loadApprover().catch(() => null)
  let persisted: boolean | null = null
  try {
    persisted = (await navigator.storage?.persisted?.()) ?? null
  } catch {
    persisted = null
  }
  store.update({ me, persisted })
}

/** This browser's approver key for the world it shows, if it has one. */
export function myKey(s: State): string | null {
  return s.me && s.world?.owner && s.me.world === s.world.owner ? s.me.key : null
}

/** This browser's entry in the registry, if it has a key for this world. */
export function myApprover(s: State): ApproverInfo | null {
  const key = myKey(s)
  return (key && s.approvers?.find((a) => a.key === key)) || null
}

async function post(path: string, body: unknown): Promise<unknown> {
  const resp = await api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const data = (await resp.json().catch(() => ({}))) as { error?: string }
  if (!resp.ok) throw new Error(data.error ?? `The citadel answered ${resp.status}.`)
  return data
}

const now = (): string => new Date().toISOString().replace(/\.\d+Z$/, 'Z')

const INVITE_ADDRESS_KEY = 'skylands.inviteAddress'

/**
 * The address an "Add an approver" link opens at: the one last chosen in
 * this browser, or the citadel's --url when it has one, or this page's
 * own. A phone often can't open the address a laptop reaches the citadel
 * at, such as 127.0.0.1.
 */
function inviteAddress(s: State): string {
  try {
    const kept = localStorage.getItem(INVITE_ADDRESS_KEY)
    const ok = kept ? citadelAddress(kept) : null
    if (ok && 'address' in ok) return ok.address
  } catch {
    // No storage: fall through.
  }
  return s.world?.url || location.origin
}

/** Keeps the address chosen for invite links, or forgets it when it is this page's own. */
function rememberInviteAddress(address: string): void {
  try {
    if (address === location.origin) localStorage.removeItem(INVITE_ADDRESS_KEY)
    else localStorage.setItem(INVITE_ADDRESS_KEY, address)
  } catch {
    // No storage: the choice lasts for this link only.
  }
}

export function approverActions(store: Store): ApproverActions {
  let expiry: ReturnType<typeof setTimeout> | undefined
  const signer = () => {
    const s = store.state
    if (!s.me || !myApprover(s)?.valid || !s.world?.owner) throw new Error('This browser doesn’t approve for this world.')
    return { kp: keyFromSeed(s.me.seed), owner: s.world.owner }
  }
  return {
    invite() {
      void (async () => {
        try {
          const { kp, owner } = signer()
          // The claim seed goes only into the link's fragment, which the browser never sends to the citadel.
          const claim = newApproverKey()
          const voucher: Signed = sign(kp, { type: 'voucher', world: owner, claim: claim.getPublicKey(), created: now() })
          const info = (await post('/api/world/vouchers', { voucher })) as VoucherInfo
          const address = inviteAddress(store.state)
          const link = `${address}/setup#approver=${seedOf(claim)}`
          claim.clear()
          store.update({ invite: { link, claim: info.claim, expires: info.expires, address }, approverError: null })
          clearTimeout(expiry)
          // Re-render when the link stops working.
          expiry = setTimeout(() => store.update({}), Math.max(0, Date.parse(info.expires) - Date.now()) + 500)
        } catch (err) {
          store.update({ approverError: `Couldn’t make a link: ${(err as Error).message}` })
        }
      })()
    },
    inviteAt(address) {
      const inv = store.state.invite
      if (!inv) return
      rememberInviteAddress(address)
      store.update({ invite: { ...inv, link: linkAt(inv.link, address), address, other: undefined } })
    },
    inviteOther(other) {
      const inv = store.state.invite
      if (!inv) return
      store.update({ invite: { ...inv, other: other ?? undefined } })
    },
    closeInvite() {
      clearTimeout(expiry)
      store.update({ invite: null })
    },
    askRevoke(key) {
      store.update({ revoking: key, approverError: null })
    },
    revoke(key) {
      void (async () => {
        try {
          const { kp, owner } = signer()
          const revocation = sign(kp, { type: 'revocation', world: owner, key, created: now() })
          await post('/api/world/revocations', { revocation })
          store.update({ revoking: null, approverError: null })
        } catch (err) {
          store.update({ approverError: `Couldn’t revoke: ${(err as Error).message}` })
        }
      })()
    },
  }
}

/** Asks the browser to keep this origin's storage, and records whether it will. */
export async function keepThisBrowser(store: Store): Promise<void> {
  store.update({ persisted: await keepStorage() })
}

/**
 * Signs a decision built for this world with this browser's approver key,
 * or says why it can't: it has no approver key for this world, or the key
 * no longer approves.
 */
function signAs(s: State, build: (world: string) => Decision): DecisionSignature | string {
  const mine = myApprover(s)
  if (!s.me || !mine || !s.world?.owner) return 'This browser isn’t an approver for this world, so it can’t do that here.'
  if (!mine.valid) return 'This browser no longer approves for this world, so it can’t do that here.'
  return signDecision(keyFromSeed(s.me.seed), build(s.world.owner))
}

/** This browser's signature over a decision on a waiting request, as the daemon will check it, or why it can't sign. */
export function signFor(s: State, x: Session, p: Permission, decision: 'allow' | 'deny', option: string, inputHash: string): DecisionSignature | string {
  return signAs(s, (world) => ({ v: DECISION_VERSION, kind: 'toolcall', world, host: x.host, session: x.id, request: p.requestId, decision, option: decision === 'allow' ? option : '', input: inputHash }))
}

/** This browser's signature over an answer to a question, or a decline when answers is null. */
export function signAnswer(s: State, x: Session, q: Question, answers: Record<string, unknown> | null): DecisionSignature | string {
  return signAs(s, (world) => ({
    v: DECISION_VERSION,
    kind: 'answer',
    world,
    host: x.host,
    session: x.id,
    question: q.questionId,
    form: formHash(q.message, q.fields),
    declined: answers === null,
    answers: answers === null ? '' : answersHash(answers),
  }))
}

/** This browser's signature over turning a session's trust on or off, from the revision it shows. */
export function signTrust(s: State, x: Session, trusted: boolean): DecisionSignature | string {
  return signAs(s, (world) => ({ v: DECISION_VERSION, kind: 'trust', world, host: x.host, session: x.id, trusted, revision: x.trustRevision ?? 0 }))
}

/** A session's rules as the daemon will keep them, and this browser's signature over them. */
export function signSessionRules(s: State, x: Session, rules: Rule[]): { rules: Rule[]; revision: number; signature: DecisionSignature | string } {
  const list = normalizeRules(rules, false)
  const revision = x.rulesRevision ?? 0
  return { rules: list, revision, signature: signAs(s, (world) => ({ v: DECISION_VERSION, kind: 'session-rules', world, host: x.host, session: x.id, rules: rulesHash(list), revision })) }
}

/** A repository's rules as the daemon will keep them, and this browser's signature over them. */
export function signRepoRules(s: State, r: Repo, rules: Rule[]): { rules: Rule[]; revision: number; signature: DecisionSignature | string } {
  const list = normalizeRules(rules, true)
  const revision = r.rulesRevision ?? 0
  return { rules: list, revision, signature: signAs(s, (world) => ({ v: DECISION_VERSION, kind: 'repo-rules', world, host: r.host, repo: r.id, rules: rulesHash(list), revision })) }
}

/**
 * A command's signature once the world exists, or nothing before it does,
 * when a daemon takes unsigned commands, or why this browser can't sign.
 */
function signOnceWorld(s: State, build: (world: string) => Decision): { signature?: DecisionSignature; error?: string } {
  if (!s.world?.owner) return {}
  const signature = signAs(s, build)
  return typeof signature === 'string' ? { error: signature } : { signature }
}

/** A new session's id: letters and digits, as the citadel takes one a browser picks. */
export function newSessionId(): string {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = crypto.getRandomValues(new Uint8Array(22))
  return Array.from(bytes, (b) => chars[b % chars.length]).join('')
}

/** This browser's signature over starting a session, as the daemon will check it. */
export function signStart(s: State, start: { host: string; session: string; repo: string; agent: string; mode: string; model: string; inCheckout: boolean; prompt: string }) {
  return signOnceWorld(s, (world) => ({ v: DECISION_VERSION, kind: 'start', world, host: start.host, session: start.session, repo: start.repo, agent: start.agent, mode: start.mode, model: start.model, inCheckout: start.inCheckout, prompt: textHash(start.prompt) }))
}

/** This browser's signature over another prompt to a session, at the revision it names. */
export function signPrompt(s: State, x: Session, text: string, revision: number) {
  return signOnceWorld(s, (world) => ({ v: DECISION_VERSION, kind: 'prompt', world, host: x.host, session: x.id, revision, prompt: textHash(text) }))
}

/** This browser's signature over a commit, merge, or pull request, at the revision it names. */
export function signChange(s: State, x: Session, action: 'commit' | 'merge' | 'pull-request', text: string, revision: number) {
  return signOnceWorld(s, (world) => ({ v: DECISION_VERSION, kind: 'change', world, host: x.host, session: x.id, revision, action, text }))
}

/** Whether this browser can sign for the world it shows. */
export function canSign(s: State): boolean {
  return !!myApprover(s)?.valid
}
