import { compose, Draft, type ComposeParts } from './composer'
import { byId } from './dom'
import { imagesRefused, startSession } from './start'
import { StartChips } from './startchips'
import { isStale } from './status'
import { level, type State } from './store'

/**
 * The command bar at the bottom of the world. It takes a work order for
 * the terrace you are on and dispatches it straight to that island's
 * daemon. The inbox's New form starts a session on any repository.
 */
export class CommandBar {
  private readonly root = byId<HTMLElement>('cmdbar')
  private readonly input = byId<HTMLInputElement>('cmdInput')
  private readonly go = byId<HTMLButtonElement>('cmdGo')
  /** The agent, mode, model, and Where chips, as the island last said what it can run. */
  private readonly chips = new StartChips({
    agent: byId<HTMLSelectElement>('agentSel'),
    modeChip: byId<HTMLElement>('modeChip'),
    mode: byId<HTMLSelectElement>('modeSel'),
    model: byId<HTMLSelectElement>('modelSel'),
    otherModel: byId<HTMLInputElement>('modelOther'),
    where: byId<HTMLSelectElement>('whereSel'),
  })
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private host = ''
  private repoId = ''
  /** The state last shown, which a start is signed against. */
  private state: State | null = null
  private busy = false
  /** The files the order mentions and the images attached to it. */
  private readonly draft = new Draft()
  private readonly compose: ComposeParts
  /** What the status line is showing: the default hint, a link problem, a dispatch error, or a dispatch result. */
  private kind: 'hint' | 'blocked' | 'error' | 'info' = 'hint'

  private static readonly HINT = 'Dispatches straight to this island’s daemon.'

  constructor() {
    // The @ list takes the keys it uses before the bar does, so picking a
    // file doesn't launch the order.
    this.compose = compose(this.input, this.draft, {
      files: () => (this.host && this.repoId ? `/api/repos/${encodeURIComponent(this.host)}/${encodeURIComponent(this.repoId)}/files` : null),
      host: () => this.host || null,
      noImages: () => (this.state ? imagesRefused(this.state.islands[this.host], this.chips.chosen().agent) : 'The world isn’t loaded yet.'),
    })
    const anchor = byId<HTMLElement>('cmdAnchor')
    anchor.prepend(this.compose.menu)
    anchor.appendChild(this.compose.attach)
    const tray = byId<HTMLElement>('cmdTray')
    tray.appendChild(this.compose.tray)
    const showTray = (): void => {
      tray.hidden = this.compose.tray.hidden
    }
    new MutationObserver(showTray).observe(this.compose.tray, { attributes: true, attributeFilter: ['hidden'] })
    showTray()
    byId<HTMLElement>('agentSel').addEventListener('change', () => this.compose.refresh())
    this.go.addEventListener('click', () => void this.submit())
    this.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void this.submit()
      }
    })
    // Editing the order clears the last dispatch's error or result.
    this.input.addEventListener('input', () => {
      if (this.kind === 'error' || this.kind === 'info') this.setWhy(CommandBar.HINT, 'hint')
    })
  }

  /** Shows the bar on a terrace or one of its files, aimed at that repo. */
  update(s: State): void {
    this.state = s
    const l = level(s)
    const repo = s.terrace ? s.repos[s.terrace] : undefined
    const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
    const show = (l === 'terrace' || l === 'file') && !!repo && !!island
    this.root.hidden = !show
    if (!show || !repo || !island) return
    // Images attached go to the island the order goes to, which may have
    // changed since they were attached.
    if (this.host !== island.host) this.draft.sendTo(island.host)
    this.host = island.host
    this.repoId = repo.id
    this.scopeTo.textContent = `${repo.name} on ${island.host}`
    this.chips.show(island)
    this.compose.refresh()
    const blocked = isStale(s)
      ? 'The citadel is unreachable, so nothing can be dispatched.'
      : !island.reachable
        ? `${island.host} is unreachable, so the order can’t be delivered yet.`
        : !this.chips.hasAgents()
          ? `${island.host} can’t run any agent yet. On it, skylands preflight says why.`
          : ''
    this.go.disabled = !!blocked || this.busy
    this.input.disabled = !!blocked
    // A link problem shows while it lasts and clears when the link is back.
    // A dispatch error stays until the order is edited.
    if (blocked) this.setWhy(blocked, 'blocked')
    else if (this.kind === 'blocked') this.setWhy(CommandBar.HINT, 'hint')
  }

  private setWhy(text: string, kind: 'hint' | 'blocked' | 'error' | 'info'): void {
    if (this.why.textContent !== text) this.why.textContent = text
    this.why.style.color = kind === 'blocked' || kind === 'error' ? 'var(--bad)' : ''
    this.kind = kind
  }

  private async submit(): Promise<void> {
    const prompt = this.input.value.trim()
    if (!prompt || this.busy || !this.host || !this.state) return
    this.busy = true
    this.go.disabled = true
    this.setWhy(this.draft.images.length ? 'Sending the images…' : 'Dispatching…', 'info')
    try {
      const host = this.host
      const carried = await this.draft.carried(prompt, host)
      if (typeof carried === 'string') {
        this.setWhy(carried, 'error')
        return
      }
      if (carried.images.length) this.setWhy('Dispatching…', 'info')
      const out = await startSession(this.state, { host, repo: this.repoId, ...this.chips.chosen(), prompt, ...carried })
      if (out.error !== undefined) {
        this.setWhy(out.error, 'error')
        return
      }
      this.input.value = ''
      this.draft.clear()
      this.setWhy('Dispatched. The agent appears on the terrace as it starts.', 'info')
    } finally {
      this.busy = false
      this.go.disabled = false
    }
  }
}
