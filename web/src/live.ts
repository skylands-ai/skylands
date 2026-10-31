import type { Fetched } from './fetchcache'
import type { Live } from './protocol'

const encoder = new TextEncoder()

/** The length of a string in UTF-8 bytes, which is how transcript offsets count. */
export function byteLength(s: string): number {
  return encoder.encode(s).length
}

/**
 * Text appended to transcripts since they were last fetched. The daemon
 * numbers each piece by where it landed in the file, so the fetched text
 * plus the pieces that follow it is the transcript right now, and a piece
 * that arrives out of place means a fetch is due.
 */
export class LiveText {
  private readonly pieces = new Map<string, Live[]>()

  push(id: string, piece: Live): void {
    const list = this.pieces.get(id) ?? []
    list.push(piece)
    this.pieces.set(id, list)
  }

  /** Drops what is known for a session, such as when nobody is reading it. */
  clear(id: string): void {
    this.pieces.delete(id)
  }

  /**
   * The transcript as it stands: the fetched text continued by the live
   * pieces that follow it. `gap` says a piece arrived beyond what is known,
   * so the fetched text is behind and should be read again.
   */
  compose(id: string, fetched: Fetched | undefined): { text: string; gap: boolean } {
    const base = fetched?.text ?? ''
    const list = this.pieces.get(id)
    if (!list?.length) return { text: base, gap: false }
    let end = fetched?.length ?? byteLength(base)
    const stale = fetched?.truncated ?? false
    let text = base
    let gap = false
    const keep: Live[] = []
    for (const p of list.sort((a, b) => a.off - b.off)) {
      const len = byteLength(p.text)
      if (p.off + len <= end) continue // already in the fetched text
      if (p.off > end) {
        gap = true
        keep.push(p)
        continue
      }
      if (stale) {
        keep.push(p)
        continue
      }
      // A piece that overlaps the end only partly is cut to what is new.
      if (p.off < end) {
        text += new TextDecoder().decode(encoder.encode(p.text).slice(end - p.off))
      } else {
        text += p.text
      }
      end = p.off + len
    }
    this.pieces.set(id, keep)
    return { text, gap }
  }
}
