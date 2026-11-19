import { el } from './dom'
import type { IssueLink } from './issue'
import type { Island } from './protocol'
import type { State } from './store'

/** What the command bar asks when no island has the repository an issue is in. */
export interface CloneAsk {
  link: IssueLink
  /** Goes on with the work on the issue, once the clone is a terrace. */
  cloned: (host: string, repoId: string) => void
}

/** Starts cloning url into path on host, signed: the addition's id, or why it couldn't start. */
export type StartClone = (host: string, path: string, url: string) => Promise<{ id: string } | { error: string }>

/** The islands a repository can be cloned onto: those reachable, whose daemons clone. */
function cloneIslands(s: State): Island[] {
  return Object.values(s.islands)
    .filter((i) => i.reachable && !!i.daemon.cloneRoot)
    .sort((a, b) => a.host.localeCompare(b.host))
}

/**
 * The dialog that clones an issue's repository onto an island, when no
 * island has it, and goes on with the work once its terrace appears. The
 * clone is signed as any addition from an island's panel is.
 */
export class CloneDialog {
  private readonly dialog = el('dialog', { class: 'newsession clonerepo', 'aria-labelledby': 'cloneTitle' })
  private readonly title = el('h2', { id: 'cloneTitle' })
  private readonly why = el('p', { class: 'ns-why' })
  private readonly island = el('select', { 'aria-label': 'Island' })
  private readonly url = el('input', { type: 'text', 'aria-label': 'Clone URL', autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false' })
  private readonly into = el('input', { type: 'text', 'aria-label': 'Clone into', autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false' })
  private readonly root = el('p', { class: 'ns-why' })
  private readonly go = el('button', { type: 'button', class: 'btn small primary', text: 'Clone' })
  private readonly note = el('span', { class: 'ns-note', role: 'status' })
  private ask: CloneAsk | null = null
  /** The clone the dialog waits on: its island, its addition's id, and its URL. */
  private running: { host: string; id: string; url: string } | null = null
  private starting = false
  private error = ''
  private islandsShown = ''
  private state: State | null = null

  constructor(private readonly startClone: StartClone) {
    const cancel = el('button', { type: 'button', class: 'btn small ghost', text: 'Cancel', onclick: () => this.dialog.close() })
    this.dialog.append(
      el('div', { class: 'ns-body' }, [
        this.title,
        this.why,
        el('div', { class: 'ns-row' }, [el('label', { class: 'chip' }, ['Island', this.island])]),
        el('div', { class: 'ns-row' }, [el('label', { class: 'chip wide' }, ['Clone URL', this.url])]),
        el('div', { class: 'ns-row' }, [el('label', { class: 'chip' }, ['Clone into', this.into])]),
        this.root,
        el('div', { class: 'ns-foot' }, [this.note, cancel, this.go]),
      ]),
    )
    document.body.appendChild(this.dialog)
    for (const type of ['input', 'change']) {
      this.dialog.addEventListener(type, () => {
        this.error = ''
        this.refresh()
      })
    }
    for (const input of [this.url, this.into]) {
      input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') void this.submit()
      })
    }
    this.go.addEventListener('click', () => void this.submit())
    // Closing stops waiting; a clone already started goes on, on its island.
    this.dialog.addEventListener('close', () => {
      this.ask = null
      this.running = null
    })
  }

  /** Opens the dialog for the issue's repository. */
  open(s: State, ask: CloneAsk): void {
    this.ask = ask
    this.running = null
    this.error = ''
    this.islandsShown = ''
    const name = `${ask.link.owner}/${ask.link.repo}`
    this.title.textContent = `Clone ${name}?`
    this.why.textContent = `No island has ${name}, where ${ask.link.ref} is.`
    this.url.value = ask.link.cloneUrl
    this.into.value = ask.link.repo
    if (!this.dialog.open) this.dialog.showModal()
    this.update(s)
    this.island.focus()
  }

  /** Follows the world while the dialog is open: which islands can clone, and the clone it waits on. */
  update(s: State): void {
    this.state = s
    if (!this.dialog.open || !this.ask) return
    const islands = cloneIslands(s)
    const key = islands.map((i) => i.host).join('|')
    if (key !== this.islandsShown) {
      this.islandsShown = key
      const keep = this.island.value
      this.island.replaceChildren(...islands.map((i) => el('option', { value: i.host, text: i.host })))
      if (islands.some((i) => i.host === keep)) this.island.value = keep
    }
    const run = this.running
    if (run) {
      const repo = Object.values(s.repos).find((r) => r.host === run.host && r.url === run.url)
      if (repo) {
        const ask = this.ask
        this.dialog.close()
        ask.cloned(repo.host, repo.id)
        return
      }
      const failed = s.islands[run.host]?.clones?.find((c) => c.id === run.id && c.error)
      if (failed) {
        this.running = null
        this.error = `Couldn’t clone it: ${failed.error}`
      }
    }
    this.refresh()
  }

  private refresh(): void {
    const s = this.state
    const island = s?.islands[this.island.value]
    const busy = this.starting || !!this.running
    this.root.textContent = island?.daemon.cloneRoot ? `Paths are relative to ${island.daemon.cloneRoot}.` : ''
    for (const c of [this.island, this.url, this.into]) c.disabled = busy
    const none = !this.islandsShown
    this.go.disabled = busy || none || !this.url.value.trim() || !this.into.value.trim()
    const text = this.error || (this.running ? `Cloning onto ${this.running.host}…` : none ? 'No island can clone it right now.' : '')
    this.note.textContent = text
    this.note.classList.toggle('bad', !!this.error || (none && !busy))
  }

  private async submit(): Promise<void> {
    const host = this.island.value
    const url = this.url.value.trim()
    const path = this.into.value.trim()
    if (this.go.disabled || !host) return
    this.starting = true
    this.error = ''
    this.refresh()
    const out = await this.startClone(host, path, url)
    this.starting = false
    if ('error' in out) this.error = out.error
    else this.running = { host, id: out.id, url }
    if (this.state) this.update(this.state)
  }
}
