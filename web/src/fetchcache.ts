import { byteLength } from './live'

/** One fetched text, such as a session's transcript or diff. */
export interface Fetched {
  text: string
  truncated: boolean
  /** The whole text's length in bytes on the host, even when truncated. */
  length: number
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
  peek(kind: 'transcript' | 'diff', host: string, id: string): Fetched | undefined {
    return this.entries.get(`${kind}/${host}/${id}`)
  }

  /**
   * Returns the cached text for one session's transcript or diff, starting a
   * fetch when there is none, when the entry is older than refreshMs, or
   * when it was fetched before `since`, the session's latest event, so the
   * text is read again after each event without polling.
   */
  get(kind: 'transcript' | 'diff', host: string, id: string, refreshMs: number, since = 0): Fetched {
    const key = `${kind}/${host}/${id}`
    let e = this.entries.get(key)
    if (!e) {
      e = { text: '', truncated: false, length: 0, loading: false, at: 0 }
      this.entries.set(key, e)
    }
    const stale = e.at === 0 || e.at < since || (refreshMs > 0 && Date.now() - e.at > refreshMs)
    if (stale && !e.loading) {
      e.loading = true
      void this.load(kind, host, id, e)
    }
    return e
  }

  private async load(kind: string, host: string, id: string, e: Fetched): Promise<void> {
    try {
      const resp = await fetch(`/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(id)}/${kind}`)
      const body = (await resp.json().catch(() => ({}))) as { text?: string; truncated?: boolean; length?: number; error?: string }
      if (!resp.ok) {
        e.error = body.error ?? `The citadel answered ${resp.status}.`
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
