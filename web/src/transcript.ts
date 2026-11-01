/** One message in a conversation, or a note the daemon left between messages, such as a restart. */
export interface Turn {
  who: 'you' | 'agent' | 'note'
  text: string
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
