import { describe, expect, it } from 'vitest'
import type { TokenUsage } from './protocol'
import { tokensBreakdown, tokensLabel } from './status'

const none: TokenUsage = { input: 0, output: 0, total: 0 }
// What a Claude Code title request costs: about the same for every session.
const title: TokenUsage = { input: 2, output: 31, cachedRead: 14758, cachedWrite: 8714, total: 23505 }

describe('tokensLabel', () => {
  it('gives the total between turns', () => {
    expect(tokensLabel(title)).toBe('23.5k tokens')
  })

  it('says the running turn is still to come, since agents count a turn only as it ends', () => {
    expect(tokensLabel(title, true)).toBe('23.5k tokens + this turn')
  })

  it('says nothing is counted yet, running or not', () => {
    expect(tokensLabel(none)).toBe('no tokens yet')
    expect(tokensLabel(none, true)).toBe('no tokens yet')
  })
})

describe('tokensBreakdown', () => {
  it('gives the exact counts', () => {
    expect(tokensBreakdown(title)).toBe(`${(23505).toLocaleString()} tokens: 2 in, 31 out, ${(14758).toLocaleString()} cache read, ${(8714).toLocaleString()} cache write`)
  })

  it('adds that the running turn is not in them yet', () => {
    expect(tokensBreakdown(title, true)).toBe(`${tokensBreakdown(title)}. The turn in progress is counted when it ends.`)
    expect(tokensBreakdown(none, true)).toBe('The turn in progress is counted when it ends.')
  })

  it('is empty when nothing was counted and no turn runs', () => {
    expect(tokensBreakdown(none)).toBe('')
  })
})
