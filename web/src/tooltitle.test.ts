import { describe, expect, it } from 'vitest'
import { titleAdds } from './tooltitle'

describe('titleAdds', () => {
  const long = 'grep -rn "x" web/src | head -40; '.repeat(6).trim()
  const cut = long.slice(0, 160) + '…'

  it('leaves out a title the line already shows whole', () => {
    expect(titleAdds('Read web/src/a.ts', 'Read web/src/a.ts', { file_path: '/w/web/src/a.ts' })).toBe(false)
    expect(titleAdds('ls\n  -la', 'ls -la', { command: 'ls -la' })).toBe(false)
    expect(titleAdds('', '', { command: 'ls' })).toBe(false)
  })

  it('shows a title the line cut short', () => {
    expect(titleAdds(long, cut, { pattern: 'x' })).toBe(true)
    expect(titleAdds(long, cut, undefined)).toBe(true)
    expect(titleAdds(long, cut, [long])).toBe(true)
  })

  it('leaves out a cut title that a field of the input holds word for word', () => {
    expect(titleAdds(long, cut, { command: long, description: 'Search' })).toBe(false)
    expect(titleAdds(long, cut, long)).toBe(false)
  })

  it('shows a cut title that the input holds only in part', () => {
    expect(titleAdds(long, cut, { command: long + ' | wc -l' })).toBe(true)
  })
})
