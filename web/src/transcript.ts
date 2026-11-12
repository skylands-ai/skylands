/** One message in a conversation, or a note the daemon left between messages, such as a restart. */
export interface Turn {
  who: 'you' | 'agent' | 'note'
  text: string
  /** On a prompt: the files it mentioned, and whether the agent got each one's contents or a link to it. */
  mentions?: { path: string; embedded: boolean }[]
  /** On a prompt: the images it carried, by name and hash. */
  images?: { name: string; hash: string }[]
}

export interface Conversation {
  turns: Turn[]
  /** How the transcript says the session ended, if it has: "Finished: end_turn", "Stopped", "Failed: ...". */
  end?: string
}

const YOU = /^#{1,2} (You|Prompt)\s*$/
const AGENT = /^#{1,2} Agent\s*$/
const END = /^\*(Finished: .*|Stopped|Failed: .*)\*\s*$/
const NOTE = /^\*(.+)\*\s*$/
/** The lines the daemon lists under a prompt: a file it mentioned, and an image it carried with the marker that names it. */
const MENTION = /^\u{1F4C4} (.+) \((contents|link)\)\s*$/u
const IMAGE = /^\u{1F5BC} (.*?) <!-- image:([0-9a-f]{64}) -->\s*$/u

/** Takes the mention and image lines off the end of a prompt, onto the turn. */
function carried(t: Turn): void {
  const lines = t.text.split('\n')
  const mentions: { path: string; embedded: boolean }[] = []
  const images: { name: string; hash: string }[] = []
  while (lines.length) {
    const line = lines[lines.length - 1]
    const m = MENTION.exec(line)
    const i = m ? null : IMAGE.exec(line)
    if (m) mentions.unshift({ path: m[1], embedded: m[2] === 'contents' })
    else if (i) images.unshift({ name: i[1], hash: i[2] })
    else if (line.trim() !== '') break
    lines.pop()
  }
  if (!mentions.length && !images.length) return
  t.text = lines.join('\n').trim()
  if (mentions.length) t.mentions = mentions
  if (images.length) t.images = images
}

/**
 * Splits a transcript into turns. The daemon writes "## You" and "## Agent"
 * headings (the first sessions wrote "# Prompt" and "# Agent"), a closing
 * line after a rule when the session ends, and after a rule in the middle
 * a note of something that happened to the session, such as the daemon
 * restarting under it.
 */
export function parseTranscript(md: string): Conversation {
  const turns: Turn[] = []
  let end: string | undefined
  let cur: Turn | null = null
  let afterRule = false
  const flush = (): void => {
    if (cur) {
      cur.text = cur.text.trim()
      if (cur.who === 'you') carried(cur)
      turns.push(cur)
    }
    cur = null
  }
  for (const line of md.split('\n')) {
    if (YOU.test(line)) {
      flush()
      cur = { who: 'you', text: '' }
      continue
    }
    if (AGENT.test(line)) {
      flush()
      cur = { who: 'agent', text: '' }
      continue
    }
    if (line.trim() === '---') {
      afterRule = true
      continue
    }
    const m = afterRule ? END.exec(line) : null
    if (m) {
      end = m[1]
      afterRule = false
      continue
    }
    const n = afterRule ? NOTE.exec(line) : null
    if (n) {
      flush()
      turns.push({ who: 'note', text: n[1] })
      afterRule = false
      continue
    }
    if (line.trim() !== '') afterRule = false
    if (cur) cur.text += line + '\n'
  }
  flush()
  return { turns, end }
}
