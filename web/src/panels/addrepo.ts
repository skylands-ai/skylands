import { cloneDir, isCloneURL } from '../cloneurl'
import { el } from '../dom'
import type { Clone } from '../protocol'
import { section, timeNode } from './parts'

/** What is typed into one island's form, which survives the panel being drawn again. */
interface Draft {
  source: string
  /** Where a clone goes, once the person has changed it from the directory the URL names. */
  into: string | null
  busy: boolean
  error: string
}

/** The islands' forms, by host. */
const drafts = new Map<string, Draft>()
let version = 0

/** Changes whenever an island's form starts adding or answers, so the panel knows to redraw. */
export function addRepoVersion(): number {
  return version
}

export interface AddRepoOptions {
  host: string
  /** The island's clone root, as its daemon reports it. */
  root: string | undefined
  /** Adds the repository at path, or clones url into path, and returns an error message, or empty. */
  add: (path: string, url: string) => Promise<string>
  redraw: () => void
}

/** The form that adds a repository to an island, by a path on the host or a clone URL. */
export function addRepoSection(o: AddRepoOptions): HTMLElement {
  let d = drafts.get(o.host)
  if (!d) {
    d = { source: '', into: null, busy: false, error: '' }
    drafts.set(o.host, d)
  }
  const draft = d
  const source = el('input', {
    type: 'text',
    value: draft.source,
    placeholder: 'Path, or a clone URL',
    'aria-label': 'Path or clone URL',
    autocomplete: 'off',
    autocapitalize: 'off',
    spellcheck: 'false',
    'data-keep': `add-repo-${o.host}`,
  }) as HTMLInputElement
  const into = el('input', { type: 'text', 'aria-label': 'Clone into', autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false', 'data-keep': `add-repo-into-${o.host}` }) as HTMLInputElement
  const intoRow = el('label', { class: 'add-repo-into' }, [el('span', { class: 'muted', text: 'Clone into' }), into])
  const button = el('button', { type: 'button', class: 'btn small' }) as HTMLButtonElement
  if (draft.into !== null) into.value = draft.into
  const error = draft.error ? el('p', { class: 'approver-error', role: 'alert', text: draft.error }) : null
  // Typing shows or hides where a clone goes, and clears what the last
  // attempt said, without drawing the panel again, which would take the
  // keyboard away.
  const sync = (): void => {
    const url = isCloneURL(source.value)
    intoRow.hidden = !url
    if (draft.into === null) into.value = url ? cloneDir(source.value) : ''
    button.textContent = draft.busy ? (url ? 'Cloning…' : 'Adding…') : url ? 'Clone' : 'Add'
    button.disabled = draft.busy || !source.value.trim() || (url && !into.value.trim())
  }
  const edited = (): void => {
    draft.error = ''
    error?.remove()
    sync()
  }
  source.addEventListener('input', () => {
    draft.source = source.value
    edited()
  })
  into.addEventListener('input', () => {
    draft.into = into.value
    edited()
  })
  const submit = async (): Promise<void> => {
    if (button.disabled) return
    const url = isCloneURL(source.value) ? source.value.trim() : ''
    const path = url ? into.value.trim() : source.value.trim()
    draft.busy = true
    draft.error = ''
    version++
    o.redraw()
    const err = await o.add(path, url)
    draft.busy = false
    draft.error = err
    if (!err) {
      draft.source = ''
      draft.into = null
    }
    version++
    o.redraw()
  }
  for (const input of [source, into]) {
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') void submit()
    })
  }
  button.addEventListener('click', () => void submit())
  sync()
  return section('Add a repository', [
    o.root ? el('p', { class: 'muted add-repo-root', text: `Paths are relative to ${o.root}.` }) : null,
    el('div', { class: 'add-repo' }, [source, intoRow, button]),
    error,
  ])
}

/** A clone on an island that isn't a terrace yet: running, or why it failed. */
export function cloneRow(c: Clone): HTMLElement {
  const name = c.path.split('/').filter(Boolean).pop() ?? c.path
  return el('div', { class: 'row' }, [
    el('span', { class: 'grow' }, [
      el('span', { style: 'font-weight:600', text: name }),
      c.error
        ? el('span', { class: 'sub note-line warn', text: `Couldn’t clone ${c.url}: ${c.error}` })
        : el('span', { class: 'sub' }, [`Cloning ${c.url}, started `, timeNode(c.started)]),
    ]),
  ])
}
