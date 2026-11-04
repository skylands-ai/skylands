/**
 * Writes a value as RFC 8785 canonical JSON (JCS): no whitespace, object
 * properties sorted by their UTF-16 code units, and strings and numbers as
 * JSON.stringify writes them, which is what JCS specifies. It throws on
 * what isn't I-JSON: numbers that aren't finite, and strings with lone
 * surrogates. internal/canonical writes the same bytes in Go.
 */
export function canonicalize(v: unknown): string {
  if (v === null || typeof v === 'boolean') return JSON.stringify(v)
  if (typeof v === 'number') {
    if (!Number.isFinite(v)) throw new Error('canonical JSON has no NaN or Infinity')
    return JSON.stringify(v)
  }
  if (typeof v === 'string') return quote(v)
  if (Array.isArray(v)) return `[${v.map(canonicalize).join(',')}]`
  if (typeof v === 'object') {
    const o = v as Record<string, unknown>
    // The default sort compares UTF-16 code units, as JCS sorts.
    const keys = Object.keys(o)
      .filter((k) => o[k] !== undefined)
      .sort()
    return `{${keys.map((k) => `${quote(k)}:${canonicalize(o[k])}`).join(',')}}`
  }
  throw new Error(`canonical JSON has no ${typeof v}`)
}

/** A string as JSON, refusing lone surrogates, which JSON.stringify would escape instead. */
function quote(s: string): string {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c >= 0xd800 && c <= 0xdbff) {
      const next = s.charCodeAt(i + 1)
      if (!(next >= 0xdc00 && next <= 0xdfff)) throw new Error('canonical JSON has no lone surrogates')
      i++
    } else if (c >= 0xdc00 && c <= 0xdfff) {
      throw new Error('canonical JSON has no lone surrogates')
    }
  }
  return JSON.stringify(s)
}
