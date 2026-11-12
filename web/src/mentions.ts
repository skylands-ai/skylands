/**
 * Mentioning files in a prompt: typing @ lists the repository's files,
 * picking one puts @ and its path in the text, and a mention goes with the
 * prompt only while that text is still in it.
 */

/** The @ being typed at the caret, and what follows it so far, or null when the caret isn't in one. */
export function typingMention(value: string, caret: number): { at: number; query: string } | null {
  const before = value.slice(0, caret)
  const at = before.lastIndexOf('@')
  if (at < 0) return null
  // An @ starts a mention at the start of the text or after a space or
  // an opening bracket or quote, so an email address isn't one.
  if (at > 0 && !/[\s([{"'`]/.test(before[at - 1])) return null
  const query = before.slice(at + 1)
  if (/\s/.test(query)) return null
  return { at, query }
}

/** The text with the mention being typed at `at` replaced by @path and a space, and where the caret goes after it. */
export function pickMention(value: string, at: number, caret: number, path: string): { value: string; caret: number } {
  const inserted = `@${path} `
  const rest = value.slice(caret).replace(/^\S*/, '').replace(/^ /, '')
  return { value: value.slice(0, at) + inserted + rest, caret: at + inserted.length }
}

/**
 * The mentions a prompt carries: the picked paths whose @path is still in
 * the text, once each, in the order the text has them, as the daemon
 * orders them.
 */
export function mentionsIn(text: string, picked: string[]): string[] {
  const kept = [...new Set(picked)].filter((p) => text.includes(`@${p}`))
  return kept.sort((a, b) => text.indexOf(`@${a}`) - text.indexOf(`@${b}`))
}

/** "512 bytes", "12 KB", "3.4 MB". */
export function sizeText(n: number): string {
  if (n < 1024) return `${n} bytes`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
