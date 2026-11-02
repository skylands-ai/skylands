import { byteLength } from './live'
import type { Changes, ToolCallDetail } from './protocol'

/** What can be fetched from a host about one session. A tool call is named by its id. */
export type FetchKind = 'transcript' | 'diff' | 'changes' | 'toolcall'

/** One fetched text, such as a session's transcript or diff, or its changes, or one of its tool calls. */
export interface Fetched {
  text: string
  truncated: boolean
  /** The whole text's length in bytes on the host, even when truncated. */
  length: number
  /** The answer to a changes fetch, which is not a text. */
  changes?: Changes
  /** The answer to a tool call fetch. */
  tool?: ToolCallDetail
  error?: string
  loading: boolean
  /** When it was last fetched, in ms since the epoch, or 0 before the first fetch. */
  at: number
}

/**
 * Texts the citadel fetches from a host on demand. They are not part of
 * the world's state and are kept only while someone is looking, so they
 * live here rather than in the store.
 */
export class FetchCache {
  private readonly entries = new Map<string, Fetched>()

  constructor(private readonly onChange: () => void) {}

  /** Returns the cached entry without fetching, or undefined when nothing was ever asked for. */
  peek(kind: FetchKind, host: string, id: string, sub = ''): Fetched | undefined {
    return this.entries.get(`${kind}/${host}/${id}/${sub}`)
  }

  /**
   * Returns the cached text for one session's transcript or diff, starting a
   * fetch when there is none, when the entry is older than refreshMs, or
   * when it was fetched before `since`, the session's latest event, so the
   * text is read again after each event without polling. For a tool call,
   * `sub` is the call's id.
   */
  get(kind: FetchKind, host: string, id: string, refreshMs: number, since = 0, sub = ''): Fetched {
    const key = `${kind}/${host}/${id}/${sub}`
    let e = this.entries.get(key)
    if (!e) {
      e = { text: '', truncated: false, length: 0, loading: false, at: 0 }
      this.entries.set(key, e)
    }
    const stale = e.at === 0 || e.at < since || (refreshMs > 0 && Date.now() - e.at > refreshMs)
    if (stale && !e.loading) {
      e.loading = true
      void this.load(kind, host, id, sub, e)
    }
    return e
  }

  private async load(kind: FetchKind, host: string, id: string, sub: string, e: Fetched): Promise<void> {
    try {
      const base = `/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(id)}`
      const resp = await fetch(kind === 'toolcall' ? `${base}/tools/${encodeURIComponent(sub)}` : `${base}/${kind}`)
      const body = (await resp.json().catch(() => ({}))) as { text?: string; truncated?: boolean; length?: number; error?: string }
      if (!resp.ok) {
        e.error = body.error ?? `The citadel answered ${resp.status}.`
      } else if (kind === 'changes') {
        e.changes = body as unknown as Changes
        e.error = undefined
      } else if (kind === 'toolcall') {
        e.tool = body as unknown as ToolCallDetail
        e.error = undefined
      } else {
        e.text = body.text ?? ''
        e.truncated = !!body.truncated
        e.length = body.length ?? byteLength(e.text)
        e.error = undefined
      }
    } catch (err) {
      e.error = (err as Error).message
    } finally {
      e.loading = false
      e.at = Date.now()
      this.onChange()
    }
  }
}
