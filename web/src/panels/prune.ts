import { el } from '../dom'
import type { Session } from '../protocol'
import { leftBehind, sessionLabel } from '../status'
import { note, section } from './parts'

/** What each removal from a panel came to, by session id: empty when the worktree went, or why it was kept. */
const outcomes = new Map<string, string>()
/** The panels, by scope, whose removals are under way. */
const busy = new Set<string>()
let version = 0

/** Changes whenever a removal from a panel starts, ends, or answers, so the panel knows to redraw. */
export function pruneVersion(): number {
  return version
}

export interface PruneOptions {
  /** The host, or the host and repository, the panel shows. */
  scope: string
  canSign: boolean
  /** The island can't be reached, so nothing can be removed now. */
  down: boolean
  /** Removes one session's worktree, and its branch when git finds it merged. Resolves with an error message, or empty. */
  remove: (s: Session) => Promise<string>
  redraw: () => void
}

/**
 * The panel's offer to remove what ended sessions left behind: each
 * worktree, and its branch when git finds it merged, one signed removal at
 * a time, as "skylands session prune" does on the host. A worktree with
 * uncommitted changes is kept, and so is a branch with unmerged commits,
 * and the list says which and why. Null when there is nothing to show.
 */
export function pruneSection(sessions: Session[], o: PruneOptions): HTMLElement | null {
  const left = leftBehind(sessions)
  const tried = sessions.filter((s) => outcomes.has(s.id))
  if (!left.length && !tried.length) return null
  const kids: HTMLElement[] = []
  const running = busy.has(o.scope)
  if (left.length) {
    const n = left.length
    kids.push(
      el('p', {
        class: 'muted',
        style: 'margin:0 0 6px;font-size:12px',
        text: `${n} ended ${n === 1 ? 'session still has its worktree' : 'sessions still have their worktrees'}. A worktree with uncommitted changes stays, and so does a branch with commits that aren’t merged.`,
      }),
    )
    if (!o.canSign) {
      kids.push(note('Only an approver for this world can remove them here. On the host, run: skylands session prune'))
    } else {
      const remove = el('button', { type: 'button', class: 'btn small', text: running ? 'Removing…' : n === 1 ? 'Remove it' : 'Remove them', onclick: () => void removeAll(left, o) }) as HTMLButtonElement
      remove.disabled = running || o.down
      kids.push(el('div', { class: 'acts' }, [remove]))
    }
  }
  for (const s of tried) {
    const err = outcomes.get(s.id) ?? ''
    const what = err ? `Kept: ${err}` : s.branchKept ? 'Removed. The branch is kept: it has commits that aren’t merged.' : 'Removed.'
    kids.push(
      el('div', { class: 'row' }, [
        el('span', { class: 'grow' }, [el('span', { style: 'font-weight:600', text: sessionLabel(s, 48) }), el('span', { class: 'sub', style: err ? 'color:var(--bad)' : '', text: what })]),
      ]),
    )
  }
  return section('Worktrees', kids)
}

async function removeAll(left: Session[], o: PruneOptions): Promise<void> {
  if (busy.has(o.scope)) return
  busy.add(o.scope)
  for (const s of left) outcomes.delete(s.id)
  version++
  o.redraw()
  try {
    for (const s of left) {
      outcomes.set(s.id, await o.remove(s))
      version++
      o.redraw()
    }
  } finally {
    busy.delete(o.scope)
    version++
    o.redraw()
  }
}
