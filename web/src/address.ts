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

/** The command that makes a host's CLI an approver with an invite link, quoted for a shell. */
export function cliCommand(link: string): string {
  return `skylands approver add '${link.replace(/'/g, `'\\''`)}'`
}

/**
 * Where a page at another loopback name for the citadel, such as
 * localhost for 127.0.0.1, moves: the same page at the citadel's own
 * address, or null to stay. A browser keeps its approver key for one
 * origin, so it finds it only there. Only the browser knows the address
 * it's at; a proxy in front of the citadel may send another.
 */
export function loopbackMove(canonical: string | undefined, here: URL): string | null {
  if (!canonical) return null
  let home: URL
  try {
    home = new URL(canonical)
  } catch {
    return null
  }
  if (home.host === here.host || home.protocol !== here.protocol || home.port !== here.port) return null
  if (!isLoopback(home.hostname) || !isLoopback(here.hostname)) return null
  return home.origin + here.pathname + here.search + here.hash
}

function isLoopback(hostname: string): boolean {
  const h = hostname.toLowerCase()
  return h === 'localhost' || h === '[::1]' || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h)
}

/**
 * Moves this page to the citadel's own address when it's at another
 * loopback name for it, and says whether it did. Any other page stays
 * without asking the citadel.
 */
export async function moveToCitadel(): Promise<boolean> {
  if (!isLoopback(location.hostname)) return false
  try {
    const resp = await fetch('/api/world', { cache: 'no-store' })
    const w = resp.ok ? ((await resp.json()) as { canonical?: string }) : null
    const to = loopbackMove(w?.canonical, new URL(location.href))
    if (!to) return false
    location.replace(to)
    return true
  } catch {
    return false
  }
}
