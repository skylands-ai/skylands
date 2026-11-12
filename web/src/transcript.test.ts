import { describe, expect, it } from 'vitest'
import { parseTranscript } from './transcript'

const hash = 'ab'.repeat(32)

describe('parseTranscript', () => {
  it('takes the files a prompt mentioned and the images it carried off its text', () => {
    const md = `## You\n\nLook at @src/a.go and @docs/big.md\n\n\u{1F4C4} src/a.go (contents)\n\u{1F4C4} docs/big.md (link)\n\u{1F5BC} screen shot.png <!-- image:${hash} -->\n\n## Agent\n\nDone.\n`
    const { turns } = parseTranscript(md)
    expect(turns).toEqual([
      {
        who: 'you',
        text: 'Look at @src/a.go and @docs/big.md',
        mentions: [
          { path: 'src/a.go', embedded: true },
          { path: 'docs/big.md', embedded: false },
        ],
        images: [{ name: 'screen shot.png', hash }],
      },
      { who: 'agent', text: 'Done.' },
    ])
  })

  it('leaves a prompt that carried nothing as it was', () => {
    const { turns } = parseTranscript('## You\n\nfix it\n\n## Agent\n\nok\n')
    expect(turns[0]).toEqual({ who: 'you', text: 'fix it' })
  })

  it('keeps a line like a mention in the middle of a prompt’s text', () => {
    const { turns } = parseTranscript('## You\n\n\u{1F4C4} notes (link)\nis what I called it\n\n## Agent\n\n')
    expect(turns[0]).toEqual({ who: 'you', text: '\u{1F4C4} notes (link)\nis what I called it' })
  })
})
