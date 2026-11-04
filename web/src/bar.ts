import { byId, el } from './dom'
import type { Island } from './protocol'
import { agentLabel, isStale } from './status'
import { level, type State } from './store'

/**
 * The models the chip lists for each agent, newest first. Null means the
 * agent has no model choice; an empty list, or an agent not here, gets its
 * default and a model id typed by hand. Codex lists none: which models it
 * offers depends on the account it is logged in with. A model the agent
 * does not take stops the session from starting, with the ones it offers.
 */
const MODELS: Record<string, string[] | null> = {
  fake: null,
  claude: [
    'claude-opus-5-5',
    'claude-fable-5-1',
    'claude-sonnet-5-5',
    'claude-haiku-5-5',
    'claude-haiku-4-5-20251001',
    'claude-sonnet-5',
    'claude-opus-5',
    'claude-fable-5',
    'claude-opus-4-8',
    'claude-opus-4-7',
    'claude-opus-4-6',
    'claude-sonnet-4-6',
  ],
  codex: [],
}

/** The agents in the order the chip lists them, offered as they are until the island says which it can run. */
const AGENTS = ['fake', 'claude', 'codex']

function agentText(name: string): string {
  return name === 'fake' ? 'Fake agent, built in' : agentLabel(name)
}

/** Known agents in their usual order, then any others by name. */
function byOrder(a: string, b: string): number {
  const ia = AGENTS.indexOf(a)
  const ib = AGENTS.indexOf(b)
  if (ia !== ib) return (ia < 0 ? AGENTS.length : ia) - (ib < 0 ? AGENTS.length : ib)
  return a.localeCompare(b)
}

/**
 * The command bar at the bottom of the world. It takes a work order for
 * the terrace you are on and dispatches it straight to that island's
 * daemon. Choosing a host or repo for an order comes later.
 */
export class CommandBar {
  private readonly root = byId<HTMLElement>('cmdbar')
  private readonly input = byId<HTMLInputElement>('cmdInput')
  private readonly go = byId<HTMLButtonElement>('cmdGo')
  /** The agents the island can run, as its daemon last checked. */
  private readonly agent = byId<HTMLSelectElement>('agentSel')
  /** The chosen agent's models, every one listed whatever is chosen; each agent's last choice stays chosen. */
  private readonly model = byId<HTMLSelectElement>('modelSel')
  /** A model id typed by hand, shown while the model is "Other". */
  private readonly otherModel = byId<HTMLInputElement>('modelOther')
  private readonly where = byId<HTMLSelectElement>('whereSel')
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private host = ''
  private repoId = ''
  private busy = false
  /** The agents the agent chip lists, so it is rebuilt only when they change. */
  private agentsShown: string | null = null
  /** The agent the model chip lists models for. */
  private modelsFor: string | null = null
  /** Each agent's last model choice, kept while another agent is chosen. */
  private readonly lastModel = new Map<string, { value: string; other: string }>()
  /** What the status line is showing: the default hint, a link problem, a dispatch error, or a dispatch result. */
  private kind: 'hint' | 'blocked' | 'error' | 'info' = 'hint'

  private static readonly HINT = 'Dispatches straight to this island\u2019s daemon.'
  private static readonly OTHER = 'other'

  constructor() {
    this.syncAgents(null)
    this.agent.addEventListener('change', () => this.syncModels())
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
    this.syncAgents(island)
    const blocked = isStale(s)
      ? 'The citadel is unreachable, so nothing can be dispatched.'
      : !island.reachable
        ? `${island.host} is unreachable, so the order can\u2019t be delivered yet.`
        : !this.agent.options.length
          ? `${island.host} can\u2019t run any agent yet. On it, skylands preflight says why.`
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

  /**
   * Lists the agents the island can run, or every known one until its
   * daemon has said, keeping the chosen agent when it is still listed.
   */
  private syncAgents(island: Island | null): void {
    const names = (island?.agents ? island.agents.filter((a) => a.available).map((a) => a.name) : [...AGENTS]).sort(byOrder)
    const key = names.join(' ')
    if (key === this.agentsShown) return
    this.agentsShown = key
    const keep = this.agent.value
    this.agent.replaceChildren(...names.map((n) => el('option', { value: n, text: agentText(n) })))
    if (names.includes(keep)) this.agent.value = keep
    this.syncModels()
  }

  /** Lists the chosen agent's models, bringing back its last choice. */
  private syncModels(): void {
    const agent = this.agent.value
    if (agent === this.modelsFor) return
    if (this.modelsFor !== null) this.lastModel.set(this.modelsFor, { value: this.model.value, other: this.otherModel.value })
    this.modelsFor = agent
    const models = Object.hasOwn(MODELS, agent) ? MODELS[agent] : []
    const options = [el('option', { value: '', text: 'Agent’s default' })]
    if (models) {
      options.push(...models.map((m) => el('option', { value: m, text: m })))
      options.push(el('option', { value: CommandBar.OTHER, text: 'Other model id…' }))
    }
    this.model.replaceChildren(...options)
    this.model.disabled = !models
    const last = this.lastModel.get(agent)
    this.model.value = last && options.some((o) => o.value === last.value) ? last.value : ''
    this.otherModel.value = last?.other ?? ''
    this.otherModel.hidden = this.model.value !== CommandBar.OTHER
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
