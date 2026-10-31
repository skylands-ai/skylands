/** One message in a conversation. */
export interface Turn {
  who: 'you' | 'agent'
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

/**
 * Splits a transcript into turns. The daemon writes "## You" and "## Agent"
 * headings (the first sessions wrote "# Prompt" and "# Agent"), and a
 * closing line after a rule when the session ends.
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
    if (line.trim() !== '') afterRule = false
    if (cur) cur.text += line + '\n'
  }
  flush()
  return { turns, end }
}
