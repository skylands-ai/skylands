import { byId } from './dom'
import { isStale } from './status'
import { level, type State } from './store'

/**
 * The command bar at the bottom of the world. It takes a work order for
 * the terrace you are on and dispatches it straight to that island's
 * daemon. Choosing a host or repo for an order comes later.
 */
export class CommandBar {
  private readonly root = byId<HTMLElement>('cmdbar')
  private readonly input = byId<HTMLInputElement>('cmdInput')
  private readonly go = byId<HTMLButtonElement>('cmdGo')
  private readonly agent = byId<HTMLSelectElement>('agentSel')
  /** Every model stays listed whatever is chosen, and the last choice stays chosen. */
  private readonly model = byId<HTMLSelectElement>('modelSel')
  /** A model id typed by hand, shown while the model is "Other". */
  private readonly otherModel = byId<HTMLInputElement>('modelOther')
  private readonly where = byId<HTMLSelectElement>('whereSel')
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private host = ''
  private repoId = ''
  private busy = false
  /** What the status line is showing: the default hint, a link problem, a dispatch error, or a dispatch result. */
  private kind: 'hint' | 'blocked' | 'error' | 'info' = 'hint'

  private static readonly HINT = 'Dispatches straight to this island\u2019s daemon.'
  private static readonly OTHER = 'other'

  constructor() {
    this.model.addEventListener('change', () => {
      const other = this.model.value === CommandBar.OTHER
      this.otherModel.hidden = !other
      if (other) this.otherModel.focus()
    })
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
    const l = level(s)
    const repo = s.terrace ? s.repos[s.terrace] : undefined
    const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
    const show = (l === 'terrace' || l === 'file') && !!repo && !!island
    this.root.hidden = !show
    if (!show || !repo || !island) return
    this.host = island.host
    this.repoId = repo.id
    this.scopeTo.textContent = `${repo.name} on ${island.host}`
    const blocked = isStale(s) ? 'The citadel is unreachable, so nothing can be dispatched.' : !island.reachable ? `${island.host} is unreachable, so the order can\u2019t be delivered yet.` : ''
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

  /** The model for the next session, or empty for the agent's default. */
  private chosenModel(): string {
    return this.model.value === CommandBar.OTHER ? this.otherModel.value.trim() : this.model.value
  }

  private async submit(): Promise<void> {
    const prompt = this.input.value.trim()
    if (!prompt || this.busy || !this.host) return
    this.busy = true
    this.go.disabled = true
    this.setWhy('Dispatching\u2026', 'info')
    try {
      const resp = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ host: this.host, repoId: this.repoId, agent: this.agent.value, model: this.chosenModel(), prompt, inCheckout: this.where.value === 'checkout' }),
      })
      const body = (await resp.json().catch(() => ({}))) as { error?: string }
      if (!resp.ok) {
        this.setWhy(body.error ?? `The citadel answered ${resp.status}.`, 'error')
        return
      }
      this.input.value = ''
      this.setWhy('Dispatched. The agent appears on the terrace as it starts.', 'info')
    } catch (err) {
      this.setWhy(`Could not reach the citadel: ${(err as Error).message}`, 'error')
    } finally {
      this.busy = false
      this.go.disabled = false
    }
  }
}
