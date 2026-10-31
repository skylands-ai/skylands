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
  private readonly model = byId<HTMLInputElement>('modelInput')
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private host = ''
  private repoId = ''
  private busy = false

  constructor() {
    this.go.addEventListener('click', () => void this.submit())
    this.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void this.submit()
      }
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
    if (blocked) this.setWhy(blocked, true)
    else if (!this.busy && this.why.dataset.error !== '1') this.setWhy('Dispatches straight to this island\u2019s daemon.', false)
  }

  private setWhy(text: string, error: boolean): void {
    this.why.textContent = text
    this.why.style.color = error ? 'var(--bad)' : ''
    this.why.dataset.error = error ? '1' : ''
  }

  private async submit(): Promise<void> {
    const prompt = this.input.value.trim()
    if (!prompt || this.busy || !this.host) return
    this.busy = true
    this.go.disabled = true
    this.setWhy('Dispatching\u2026', false)
    try {
      const resp = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ host: this.host, repoId: this.repoId, agent: this.agent.value, model: this.model.value.trim(), prompt }),
      })
      const body = (await resp.json().catch(() => ({}))) as { error?: string }
      if (!resp.ok) {
        this.setWhy(body.error ?? `The citadel answered ${resp.status}.`, true)
        return
      }
      this.input.value = ''
      this.setWhy('Dispatched. The agent appears on the terrace as it starts.', false)
    } catch (err) {
      this.setWhy(`Could not reach the citadel: ${(err as Error).message}`, true)
    } finally {
      this.busy = false
      this.go.disabled = false
    }
  }
}
