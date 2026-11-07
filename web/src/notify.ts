import type { Session } from './protocol'
import type { InboxRoute } from './route'
import type { State } from './store'
import { agentLabel } from './status'

/**
 * Something that waits on a person and is worth a notification: a tool
 * call to approve or deny, or a question to answer. An agent that stopped
 * for a reply is counted in the Inbox but isn't notified.
 */
export interface Waiting {
  /** The request or question it is, which tags the notification so another tab's replaces it. */
  tag: string
  /** Where it is in the inbox, which clicking the notification opens. */
  route: InboxRoute
  title: string
  body: string
}

/** What a tool call of this kind wants, in words that name no input. */
function wants(kind: string | undefined): string {
  switch (kind) {
    case 'execute':
      return 'wants to run a command'
    case 'edit':
      return 'wants to edit a file'
    case 'delete':
      return 'wants to delete a file'
    case 'move':
      return 'wants to move a file'
    case 'read':
      return 'wants to read a file'
    case 'search':
      return 'wants to search'
    case 'fetch':
      return 'wants to fetch from the web'
    case 'switch_mode':
      return 'wants to switch modes'
    default:
      return 'wants your approval'
  }
}

/** Who is waiting, and where: "Claude Code in storefront on workstation". */
export function who(s: State, x: Session): string {
  return `${agentLabel(x.agent)} in ${s.repos[x.repoId]?.name ?? x.repoId} on ${x.host}`
}

/**
 * What waits on a person across the world, as notifications would say it.
 * They name the agent, repository, and island, and never a tool's input, a
 * file's contents, or a command.
 */
export function waitingItems(s: State): Waiting[] {
  const out: Waiting[] = []
  for (const x of Object.values(s.sessions)) {
    for (const p of x.pending) {
      const route: InboxRoute = { host: x.host, session: x.id, item: { kind: 'request', id: p.requestId } }
      out.push({ tag: `request:${p.requestId}`, route, title: `${who(s, x)} ${wants(p.kind)}`, body: 'Approve or deny it in Skylands.' })
    }
    for (const q of x.questions) {
      const route: InboxRoute = { host: x.host, session: x.id, item: { kind: 'question', id: q.questionId } }
      out.push({ tag: `question:${q.questionId}`, route, title: `${who(s, x)} has a question`, body: 'Answer it in Skylands.' })
    }
  }
  return out
}

/** A notification shown, which can be closed. */
export interface Shown {
  close(): void
}

/** Where notifications go: the browser's, or a test's. */
export interface Notices {
  /** Whether this browser will show them now: turned on here, and allowed. */
  allowed(): boolean
  /** Whether the page is out of sight, as in a background tab. */
  hidden(): boolean
  show(w: Waiting, onClick: () => void): Shown
}

/**
 * Notifies when something starts waiting on a person while the page is out
 * of sight, one notification per request or question, and closes each once
 * it is answered, here or anywhere else.
 */
export class Notifier {
  /** What waited when last observed, so only what is new notifies. */
  private seen = new Set<string>()
  private shown = new Map<string, Shown>()
  /** Whether the citadel's first snapshot has been seen: what waited before the page opened doesn't notify. */
  private started = false

  constructor(
    private notices: Notices,
    private open: (route: InboxRoute) => void,
  ) {}

  observe(s: State): void {
    // Until the first snapshot, the page knows of nothing.
    if (s.approvers === null) return
    const items = waitingItems(s)
    const now = new Set(items.map((w) => w.tag))
    const allowed = this.notices.allowed()
    for (const [tag, n] of this.shown) {
      if (!now.has(tag) || !allowed) {
        n.close()
        this.shown.delete(tag)
      }
    }
    for (const w of items) {
      if (this.seen.has(w.tag)) continue
      this.seen.add(w.tag)
      if (!this.started || !allowed || !this.notices.hidden()) continue
      const n = this.notices.show(w, () => {
        n.close()
        this.shown.delete(w.tag)
        this.open(w.route)
      })
      this.shown.set(w.tag, n)
    }
    for (const tag of this.seen) if (!now.has(tag)) this.seen.delete(tag)
    this.started = true
  }
}

/** Whether this page can offer notifications, or why not. */
export function notifySupport(): 'ok' | 'unsupported' | 'insecure' {
  if (typeof Notification === 'undefined') return 'unsupported'
  if (!window.isSecureContext) return 'insecure'
  return 'ok'
}

/** The browser's notifications, while turned on here and allowed. */
export function browserNotices(on: () => boolean): Notices {
  return {
    allowed: () => on() && notifySupport() === 'ok' && Notification.permission === 'granted',
    hidden: () => document.visibilityState !== 'visible',
    show(w, onClick) {
      try {
        const n = new Notification(w.title, { body: w.body, tag: w.tag })
        n.onclick = () => {
          window.focus()
          onClick()
        }
        return n
      } catch {
        // A phone's browser shows a page's notifications only through a
        // service worker, and refuses to make one this way.
        return { close() {} }
      }
    },
  }
}
