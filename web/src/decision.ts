import type { KeyPair } from '@nats-io/nkeys'
import { sha256 } from '@noble/hashes/sha2.js'
import { toBase64url } from './approver'
import { canonicalize } from './canonical'

/** The version of the decision format, which a daemon checks. */
export const DECISION_VERSION = 1

/**
 * What an approver signs to allow or deny one tool call, as
 * internal/approval's Decision. The signature covers the SHA-256 of its
 * canonical JSON; the daemon builds the same decision from its own record
 * of the request, so the signature verifies only if they agree.
 */
export interface Decision {
  v: number
  /** The world's owner key. */
  world: string
  host: string
  session: string
  request: string
  decision: 'allow' | 'deny'
  /** The option id an approval chose, and "" for a denial. */
  option: string
  /** The SHA-256 of the tool call's exact input, in lowercase hex. */
  input: string
}

/** An approver's signature over a decision, which the approval or denial carries. */
export interface DecisionSignature {
  approver: string
  sig: string
}

/** The SHA-256 of the decision's canonical JSON, which is what is signed. */
export function decisionDigest(d: Decision): Uint8Array {
  return sha256(new TextEncoder().encode(canonicalize(d)))
}

export function signDecision(kp: KeyPair, d: Decision): DecisionSignature {
  return { approver: kp.getPublicKey(), sig: toBase64url(kp.sign(decisionDigest(d))) }
}
