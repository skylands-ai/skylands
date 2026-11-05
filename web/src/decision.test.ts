import { fromSeed } from '@nats-io/nkeys'
import { bytesToHex } from '@noble/hashes/utils.js'
import { describe, expect, it } from 'vitest'
import { canonicalize } from './canonical'
import { answersHash, decisionDigest, formHash, normalizeRules, rulesHash, signDecision, type Decision, type DecisionSignature } from './decision'
import type { QuestionField, Rule } from './protocol'
import raw from '../../internal/approval/testdata/decision-vector.json?raw'

// The vector Go's tests check too: both must produce its bytes.
const v = JSON.parse(raw) as {
  seed: string
  decisions: { decision: Decision; canonical: string; digest: string; signature: DecisionSignature }[]
  form: { message: string; fields: QuestionField[]; hash: string }
  answers: { answers: Record<string, unknown>; hash: string }
  rules: { rules: Rule[]; hash: string }
}
const kp = () => fromSeed(new TextEncoder().encode(v.seed))

describe('signDecision', () => {
  it('reproduces every Go decision byte for byte', () => {
    expect(new Set(v.decisions.map((d) => d.decision.kind))).toEqual(new Set(['toolcall', 'answer', 'trust', 'session-rules', 'repo-rules']))
    for (const d of v.decisions) {
      expect(canonicalize(d.decision)).toBe(d.canonical)
      expect(bytesToHex(decisionDigest(d.decision))).toBe(d.digest)
      expect(signDecision(kp(), d.decision)).toEqual(d.signature)
    }
  })

  it('signs a different decision differently', () => {
    const d = v.decisions[0]
    expect(signDecision(kp(), { ...d.decision, session: 'another' } as Decision).sig).not.toBe(d.signature.sig)
  })
})

describe('hashes', () => {
  it('reproduces the Go form, answers, and rules hashes', () => {
    expect(formHash(v.form.message, v.form.fields)).toBe(v.form.hash)
    expect(answersHash(v.answers.answers)).toBe(v.answers.hash)
    expect(rulesHash(v.rules.rules)).toBe(v.rules.hash)
  })

  it('hashes a form the same whether its empty properties are there or not', () => {
    const bare: QuestionField[] = [{ key: 'k', type: 'string' }]
    const spelled: QuestionField[] = [{ key: 'k', type: 'string', title: '', description: '', options: [], required: false }]
    expect(formHash('m', bare)).toBe(formHash('m', spelled))
    expect(formHash('m', bare)).not.toBe(formHash('m', [{ key: 'k', type: 'string', required: true }]))
  })

  it('normalizes rules as the daemon keeps them', () => {
    const list: Rule[] = [
      { decision: ' Allow ' as Rule['decision'], kind: '', pattern: ' go test * ', source: 'repo' },
      { decision: 'deny', kind: 'Any', pattern: '  ' },
    ]
    expect(normalizeRules(list, false)).toEqual([
      { decision: 'allow', kind: '*', pattern: 'go test *', source: 'repo' },
      { decision: 'deny', kind: '*' },
    ])
    expect(normalizeRules(list, true)[0].source).toBeUndefined()
    expect(rulesHash(normalizeRules(list, false))).toBe(rulesHash([{ decision: 'allow', kind: '*', pattern: 'go test *', source: 'repo' }, { decision: 'deny', kind: '*', pattern: '' }]))
  })
})
