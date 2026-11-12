import { canSign, myApprover } from './approvers'
import { el } from './dom'
import type { Island, Repo } from './protocol'
import { repoChoices, startSession } from './start'
import { StartChips } from './startchips'
import { isStale } from './status'
import type { State } from './store'

/**
 * The inbox's New form, which starts a session on any repository
 * registered across the world with what the command bar takes: the
 * agent, its mode and model, where it works, and the first prompt. It is
 * built once and kept, so what was chosen and typed survives the inbox's
 * rebuilds, and the next New starts from it.
 */
export class NewSessionForm {
  private readonly dialog = el('dialog', { class: 'newsession', 'aria-labelledby': 'newsessionTitle' })
  private readonly repo = el('select', { 'aria-label': 'Repository' })
  private readonly island = el('span', { class: 'muted' })
  private readonly chips: StartChips
  private readonly prompt = el('textarea', { 'aria-label': 'First prompt', placeholder: 'Describe the work for the agent' })
  private readonly start = el('button', { type: 'button', class: 'btn small primary', text: 'Start' })
  private readonly note = el('span', { class: 'ns-note', role: 'status' })
  /** The state last shown, which a start is signed against. */
  private state: State | null = null
  private busy = false
  /** Why the last start failed, shown until the form is changed. */
  private error = ''
  /** The repositories the repository choice lists, and the note shown, so each is rebuilt only when it changes. */
  private reposShown: string | null = null
  private noteShown: string | null = null

  /** started opens the session's conversation once its daemon has taken the start. */
  constructor(private readonly started: (session: string) => void) {
    const modeChip = el('label', { class: 'chip' }, ['Mode'])
    const mode = el('select', { 'aria-label': 'Mode' })
    modeChip.appendChild(mode)
    const agent = el('select', { 'aria-label': 'Agent' })
    const model = el('select', { 'aria-label': 'Model' })
    const otherModel = el('input', { type: 'text', hidden: '', placeholder: 'Full model id', autocomplete: 'off', 'aria-label': 'Other model id' })
    const where = el('select', { 'aria-label': 'Where the agent works' })
    this.chips = new StartChips({ agent, modeChip, mode, model, otherModel, where })
    const cancel = el('button', { type: 'button', class: 'btn small ghost', text: 'Cancel', onclick: () => this.dialog.close() })
    this.dialog.append(
      el('div', { class: 'ns-body' }, [
        el('h2', { id: 'newsessionTitle', text: 'New conversation' }),
        el('div', { class: 'ns-row' }, [el('label', { class: 'chip' }, ['Repository', this.repo]), this.island]),
        el('div', { class: 'ns-row' }, [el('label', { class: 'chip' }, ['Agent', agent]), modeChip, el('span', { class: 'chip' }, [el('label', {}, ['Model', model]), otherModel]), el('label', { class: 'chip' }, ['Where', where])]),
        this.prompt,
        el('div', { class: 'ns-foot' }, [this.note, cancel, this.start]),
      ]),
    )
    document.body.appendChild(this.dialog)
    // Changing anything clears the last start's error, and choosing another
    // repository lists what its island can run.
    for (const type of ['input', 'change']) {
      this.dialog.addEventListener(type, () => {
        this.error = ''
        this.refresh()
      })
    }
    this.start.addEventListener('click', () => void this.submit())
    this.prompt.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        void this.submit()
      }
    })
  }

  /** Opens the form, on what was chosen last. */
  open(s: State): void {
    if (!this.dialog.open) this.dialog.showModal()
    this.update(s)
    ;(this.repo.value ? this.prompt : this.repo).focus()
  }

  /** Follows what the world says while the form is open. A phone's screen has no inbox to start from, so the form closes there. */
  update(s: State): void {
    this.state = s
    if (!this.dialog.open) return
    if (s.phone) {
      this.dialog.close()
      return
    }
    this.syncRepos(s)
    this.refresh()
  }

  /** Lists the repositories by island, keeping the chosen one while it is listed. */
  private syncRepos(s: State): void {
    const groups = repoChoices(s)
    const key = JSON.stringify(groups.map((g) => [g.host, !!s.islands[g.host]?.reachable, g.repos.map((r) => [r.id, r.name, r.path])]))
    if (key === this.reposShown) return
    this.reposShown = key
    const keep = this.repo.value
    this.repo.replaceChildren(
      ...groups.map((g) =>
        el(
          'optgroup',
          { label: s.islands[g.host]?.reachable ? g.host : `${g.host} (unreachable)` },
          g.repos.map((r) => el('option', { value: r.id, text: r.name, title: r.path })),
        ),
      ),
    )
    if (groups.some((g) => g.repos.some((r) => r.id === keep))) this.repo.value = keep
  }

  /** Why a session can't start from the form as it is, or empty. */
  private blocked(s: State, repo: Repo | undefined, island: Island | undefined): string {
    if (isStale(s)) return 'The citadel is unreachable, so nothing can start.'
    if (!repo) return 'No repository is registered yet. On an island, skylands repo register adds one.'
    if (!island?.reachable) return `${repo.host} is unreachable, so the session can’t start yet.`
    if (!this.chips.hasAgents()) return `${repo.host} can’t run any agent yet. On it, skylands preflight says why.`
    return ''
  }

  /** Brings the agent chips, the Start button, and the note up to date with the chosen repository. */
  private refresh(): void {
    const s = this.state
    if (!s) return
    const repo = s.repos[this.repo.value]
    const island = repo ? s.islands[repo.host] : undefined
    this.island.textContent = repo ? `on ${repo.host}` : ''
    this.chips.show(island ?? null)
    const blocked = this.blocked(s, repo, island)
    // Once the world exists, only an approver's browser can sign a start.
    const signs = !s.world?.owner || canSign(s)
    this.start.disabled = this.busy || !!blocked || !signs || !this.prompt.value.trim()
    if (this.busy) this.say('Starting…', false)
    else if (this.error) this.say(this.error, true)
    else if (blocked) this.say(blocked, true)
    else if (!signs) {
      const who = myApprover(s) ? 'This browser no longer approves for this world' : 'This browser isn’t an approver for this world'
      this.say(`${who}, so it can’t start a session here. Add it with an “Add an approver” link from an approver, or use your recovery file on the `, false, el('a', { href: '/setup', text: 'setup page' }), '.')
    } else this.say('⌘ Enter starts', false)
  }

  /** Shows a note, in the color of a problem when bad. */
  private say(text: string, bad: boolean, ...more: (Node | string)[]): void {
    const key = JSON.stringify([text, bad])
    if (key === this.noteShown) return
    this.noteShown = key
    this.note.replaceChildren(text, ...more)
    this.note.classList.toggle('bad', bad)
  }

  private async submit(): Promise<void> {
    const s = this.state
    const repo = s?.repos[this.repo.value]
    const prompt = this.prompt.value.trim()
    if (!s || !repo || !prompt || this.start.disabled) return
    this.busy = true
    this.refresh()
    const out = await startSession(s, { host: repo.host, repo: repo.id, ...this.chips.chosen(), prompt })
    this.busy = false
    if (out.error !== undefined) {
      this.error = out.error
      this.refresh()
      return
    }
    this.prompt.value = ''
    this.dialog.close()
    this.refresh()
    this.started(out.session)
  }
}
