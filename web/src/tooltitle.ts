/**
 * Whether a tool call's panel shows the call's whole title above its
 * input. The transcript's line shows the title on one line and cut short,
 * so the whole of it goes in the panel when that lost something, unless a
 * field of the input holds it word for word, as a shell command's does,
 * and so shows it already.
 */
export function titleAdds(title: string, label: string, input: unknown): boolean {
  if (!title || title.split(/\s+/).join(' ') === label) return false
  if (typeof input === 'string') return input !== title
  if (!input || typeof input !== 'object' || Array.isArray(input)) return true
  return !Object.values(input).includes(title)
}
