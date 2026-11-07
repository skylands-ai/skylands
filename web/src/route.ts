/**
 * Where an inbox link points: the list of what's waiting, one session's
 * waiting items, or one tool call or question. A notification links to the
 * item it names.
 */
export interface InboxRoute {
  host?: string
  session?: string
  item?: { kind: 'request' | 'question'; id: string }
}

const KINDS: Record<string, 'request' | 'question'> = { requests: 'request', questions: 'question' }

function decode(part: string): string | null {
  try {
    return decodeURIComponent(part)
  } catch {
    return null
  }
}

/**
 * The route a path names: /inbox, /inbox/<host>/<session>, or
 * /inbox/<host>/<session>/requests/<id> or /questions/<id>. Another path
 * under /inbox is the list; a path outside it is null.
 */
export function parseInboxPath(path: string): InboxRoute | null {
  if (path !== '/inbox' && !path.startsWith('/inbox/')) return null
  const parts = path.slice('/inbox'.length).split('/').filter(Boolean).map(decode)
  if (parts.some((p) => p === null || p === '')) return {}
  const [host, session, kind, id] = parts as string[]
  if (parts.length === 2) return { host, session }
  if (parts.length === 4 && KINDS[kind]) return { host, session, item: { kind: KINDS[kind], id } }
  return {}
}

/** The path for a route, the inverse of parseInboxPath. */
export function inboxPath(r: InboxRoute): string {
  if (!r.host || !r.session) return '/inbox'
  const session = `/inbox/${encodeURIComponent(r.host)}/${encodeURIComponent(r.session)}`
  if (!r.item) return session
  return `${session}/${r.item.kind === 'request' ? 'requests' : 'questions'}/${encodeURIComponent(r.item.id)}`
}
