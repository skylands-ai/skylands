import { describe, expect, it } from 'vitest'
import { askingText, becauseText, choseText, joined, leftOutText, undecidedText, type Candidate, type Placement } from './dispatch'

const sf: Candidate = { host: 'workstation', repoId: 'sf', name: 'storefront', confidence: 0.7, reasons: ['pricing in internal/pricing/', 'currency in the README'] }
const cat: Candidate = { host: 'workstation', repoId: 'cat', name: 'catalog', confidence: 0.6 }
const docs: Candidate = { host: 'laptop', repoId: 'docs', name: 'docs', confidence: 0.55 }

function placement(p: Partial<Placement>): Placement {
  return { do: 'person', why: 'nothing', ranked: [], repos: 3, asked: [], leftOut: [], ...p }
}

describe('joined', () => {
  it('joins as a sentence does', () => {
    expect(joined(['a'])).toBe('a')
    expect(joined(['a', 'b'])).toBe('a and b')
    expect(joined(['a', 'b', 'c'])).toBe('a, b, and c')
  })
})

describe('becauseText', () => {
  it('says what matched where, or how much', () => {
    expect(becauseText(sf)).toBe('Matched pricing in internal/pricing/, currency in the README')
    expect(becauseText(cat)).toBe('Matched 60% of the prompt')
  })

  it('says when a name or the only repository settled it', () => {
    expect(becauseText({ ...sf, named: true, reasons: ['the prompt names it'] })).toBe('The prompt names it')
    expect(becauseText({ ...sf, confidence: 0, reasons: ['the only repository on workstation'] }, 'only')).toBe('The only repository on workstation')
  })
})

describe('leftOutText', () => {
  it('says nothing when every island was considered', () => {
    expect(leftOutText({ leftOut: [] })).toBe('')
  })

  it('names the islands left out and why', () => {
    expect(leftOutText({ leftOut: [{ host: 'attic', why: 'unreachable' }] })).toBe('attic is unreachable, so its repositories weren’t considered.')
    expect(
      leftOutText({
        leftOut: [
          { host: 'attic', why: 'unreachable' },
          { host: 'cellar', why: 'unreachable' },
          { host: 'laptop', why: 'no answer' },
        ],
      }),
    ).toBe('attic and cellar are unreachable and laptop didn’t answer in time, so their repositories weren’t considered.')
  })
})

describe('undecidedText', () => {
  it('says why the person chooses', () => {
    expect(undecidedText(placement({ why: 'close', among: [sf, docs] }), 'any island')).toBe('storefront on workstation and docs on laptop fit about equally well.')
    expect(undecidedText(placement({ why: 'named', among: [sf, { ...sf, host: 'laptop' }] }), 'any island')).toBe('storefront is on workstation and laptop.')
    expect(undecidedText(placement({ why: 'named', among: [sf, docs] }), 'any island')).toBe('The prompt names storefront on workstation and docs on laptop.')
    expect(undecidedText(placement({ why: 'named', among: [sf] }), 'any island')).toBe('storefront is on workstation, which is unreachable.')
    expect(undecidedText(placement({ why: 'weak', among: [cat] }), 'workstation')).toBe('Nothing fits well.')
    expect(undecidedText(placement({ why: 'nothing' }), 'workstation')).toBe('Nothing fits.')
    expect(undecidedText(placement({ why: 'nothing', repos: 0 }), 'workstation')).toBe('No repository is registered on workstation yet.')
    expect(undecidedText(placement({ why: 'nothing', leftOut: [{ host: 'laptop', why: 'unreachable' }] }), 'laptop')).toBe('No island could be asked.')
  })
})

describe('the agent breaking a tie', () => {
  it('says it is choosing, and what it chose', () => {
    expect(askingText([sf, cat])).toBe('Choosing between storefront and catalog…')
    expect(choseText('claude', cat, [sf, cat])).toBe('Claude Code chose it over storefront')
  })
})
