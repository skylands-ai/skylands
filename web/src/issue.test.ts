import { describe, expect, it } from 'vitest'
import { issuePrompt, mayLinkIssue, wordsBesides, type IssueLink } from './issue'

const link: IssueLink = {
  host: 'github.com',
  owner: 'org',
  repo: 'storefront',
  number: 12,
  url: 'https://github.com/org/storefront/issues/12',
  ref: 'org/storefront#12',
  cloneUrl: 'https://github.com/org/storefront.git',
}

describe('mayLinkIssue', () => {
  it('spots what may be an issue link, for the citadel to read', () => {
    for (const t of ['https://github.com/org/storefront/issues/12', 'fix https://ghe.corp/a/b/issues/3 now']) expect(mayLinkIssue(t), t).toBe(true)
    for (const t of ['fix the pricing bug', 'https://github.com/org/storefront/pull/12', 'http://github.com/org/storefront/issues/12', 'https://github.com/org/storefront/issues']) {
      expect(mayLinkIssue(t), t).toBe(false)
    }
  })
})

describe('wordsBesides', () => {
  it('keeps what the person wrote, whatever form the link was pasted in', () => {
    expect(wordsBesides('https://github.com/org/storefront/issues/12', link)).toBe('')
    expect(wordsBesides('Fix https://github.com/org/storefront/issues/12#issuecomment-9 today', link)).toBe('Fix today')
    expect(wordsBesides('Only the API part:\nhttps://GitHub.com/org/storefront/issues/12/', link)).toBe('Only the API part:')
    // Another issue's link is the person's words, not this one.
    expect(wordsBesides('See https://github.com/org/storefront/issues/123 too', link)).toBe('See https://github.com/org/storefront/issues/123 too')
  })
})

describe('issuePrompt', () => {
  it('asks to resolve the issue, with its name, title, link, and body', () => {
    expect(issuePrompt(link.url, link, { title: ' Prices round wrong ', body: 'Totals are off by a cent.\n' })).toBe(
      'Resolve this issue.\n\nIssue org/storefront#12: Prices round wrong\nhttps://github.com/org/storefront/issues/12\n\nTotals are off by a cent.',
    )
  })

  it('puts the person’s own words first, and leaves out an empty body', () => {
    expect(issuePrompt(`Just the tests for ${link.url}`, link, { title: 'Flaky', body: '  ' })).toBe('Just the tests for\n\nIssue org/storefront#12: Flaky\nhttps://github.com/org/storefront/issues/12')
  })
})
