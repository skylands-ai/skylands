import { describe, expect, it } from 'vitest'
import { mentionsIn, pickMention, typingMention } from './mentions'

describe('typingMention', () => {
  it('finds the @ being typed and what follows it', () => {
    expect(typingMention('look at @src/ma', 15)).toEqual({ at: 8, query: 'src/ma' })
    expect(typingMention('@', 1)).toEqual({ at: 0, query: '' })
    expect(typingMention('(@a.go', 6)).toEqual({ at: 1, query: 'a.go' })
  })

  it('leaves out an email address, a finished mention, and text before the caret', () => {
    expect(typingMention('mail me@example.com', 19)).toBeNull()
    expect(typingMention('see @a.go and', 13)).toBeNull()
    expect(typingMention('no mention here', 15)).toBeNull()
    expect(typingMention('@a.go later', 0)).toBeNull()
  })
})

describe('pickMention', () => {
  it('puts @ and the path where the mention was typed, with a space after it', () => {
    expect(pickMention('look at @ma', 8, 11, 'cmd/main.go')).toEqual({ value: 'look at @cmd/main.go ', caret: 21 })
    // Whatever of the word was after the caret goes, so the path isn't doubled.
    expect(pickMention('look at @mafoo please', 8, 11, 'cmd/main.go')).toEqual({ value: 'look at @cmd/main.go please', caret: 21 })
  })
})

describe('mentionsIn', () => {
  it('keeps the picked paths still in the text, once each, in the text’s order', () => {
    const text = 'compare @b.go with @a.go, then @b.go again'
    expect(mentionsIn(text, ['a.go', 'gone.go', 'b.go', 'a.go'])).toEqual(['b.go', 'a.go'])
  })

  it('drops a mention whose text was edited away', () => {
    expect(mentionsIn('look at a.go', ['a.go'])).toEqual([])
  })
})
