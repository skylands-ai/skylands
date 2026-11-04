import { fromSeed } from '@nats-io/nkeys'
import { bytesToHex } from '@noble/hashes/utils.js'
import { describe, expect, it } from 'vitest'
import { canonicalize } from './canonical'
import { decisionDigest, signDecision, type Decision, type DecisionSignature } from './decision'
import raw from '../../internal/approval/testdata/decision-vector.json?raw'

// The vector Go's tests check too: both must produce its bytes.
const v = JSON.parse(raw) as { seed: string; decision: Decision; canonical: string; digest: string; signature: DecisionSignature }

describe('signDecision', () => {
  it('reproduces the Go vector byte for byte', () => {
    expect(canonicalize(v.decision)).toBe(v.canonical)
    expect(bytesToHex(decisionDigest(v.decision))).toBe(v.digest)
    expect(signDecision(fromSeed(new TextEncoder().encode(v.seed)), v.decision)).toEqual(v.signature)
  })

  it('signs a different decision differently', () => {
    const kp = fromSeed(new TextEncoder().encode(v.seed))
    expect(signDecision(kp, { ...v.decision, request: 'Req2' }).sig).not.toBe(v.signature.sig)
  })
})
