/**
 * An address people open a citadel at, as the citadel's --url takes one:
 * http or https with a host, and nothing after the host but a slash, since
 * the citadel serves its pages from the root. It comes back without the
 * slash, or as an error saying what's wrong.
 */
export function citadelAddress(raw: string): { address: string } | { error: string } {
  const text = raw.trim()
  let u: URL
  try {
    u = new URL(text)
  } catch {
    return { error: 'Give the whole address, starting http:// or https://.' }
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { error: 'Give the whole address, starting http:// or https://.' }
  if (!u.hostname) return { error: 'The address has no host.' }
  if (u.username || u.password || u.search || u.hash || (u.pathname !== '/' && u.pathname !== '')) {
    return { error: 'Give only the scheme, host, and port: the citadel serves its pages from the root.' }
  }
  return { address: u.origin }
}

/** An "Add an approver" link opening at address instead, with the same claim. */
export function linkAt(link: string, address: string): string {
  return `${address}/setup${new URL(link).hash}`
}
