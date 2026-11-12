import { describe, expect, it } from 'vitest'
import { MENU_MAX, menuRoom, mentionsIn, pickMention, typingMention } from './mentions'

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

describe('menuRoom', () => {
  it('opens above the box, no taller than the list grows', () => {
    expect(menuRoom({ top: 700, bottom: 744 }, { top: 0, bottom: 800 })).toEqual({ below: false, height: MENU_MAX })
  })

  it('stays inside what clips it, as a dialog does', () => {
    // The New form's prompt sits about 138px down its dialog.
    expect(menuRoom({ top: 400, bottom: 520 }, { top: 262, bottom: 625 })).toEqual({ below: false, height: 126 })
  })

  it('opens below when the room above is short and there is more below', () => {
    expect(menuRoom({ top: 60, bottom: 100 }, { top: 0, bottom: 800 })).toEqual({ below: true, height: MENU_MAX })
    expect(menuRoom({ top: 60, bottom: 100 }, { top: 0, bottom: 150 })).toEqual({ below: false, height: 48 })
  })

  it('never asks for a negative height', () => {
    expect(menuRoom({ top: 0, bottom: 800 }, { top: 0, bottom: 800 }).height).toBe(0)
  })
})
