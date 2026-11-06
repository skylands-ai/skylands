import { newSessionId, signStart } from './approvers'
import { byId, el } from './dom'
import type { Island } from './protocol'
import { api } from './signin'
import { agentLabel, isStale, modelText } from './status'
import { level, type State } from './store'

/** The agents in the order the chip lists them, offered as they are until the island says which it can run. */
const AGENTS = ['fake', 'claude', 'codex', 'goose']

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
  /**
   * The models the chosen agent offers this island's login, as its daemon
   * last read them from the agent; each agent's last choice stays chosen.
   */
  private readonly model = byId<HTMLSelectElement>('modelSel')
  /**
   * The modes the chosen agent's adapter lists, shown only for an agent
   * with more than one; each agent's last choice stays chosen.
   */
  private readonly modeChip = byId<HTMLElement>('modeChip')
  private readonly mode = byId<HTMLSelectElement>('modeSel')
  /** A model id typed by hand, shown while the model is "Other". */
  private readonly otherModel = byId<HTMLInputElement>('modelOther')
  private readonly where = byId<HTMLSelectElement>('whereSel')
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private host = ''
  private repoId = ''
  /** The state last shown, which a start is signed against. */
  private state: State | null = null
  private busy = false
  /** The agents the agent chip lists, so it is rebuilt only when they change. */
  private agentsShown: string | null = null
  /** The island whose agents the chips list. */
  private island: Island | null = null
  /** The agent the model chip lists models for. */
  private modelsFor: string | null = null
  /** The agent and models the model chip lists, so it is rebuilt only when they change. */
  private modelsShown: string | null = null
  /** Each agent's last model choice, kept while another agent is chosen. */
  private readonly lastModel = new Map<string, { value: string; other: string }>()
  /** The agent and modes the mode chip lists, and each agent's last mode choice. */
  private modesShown: string | null = null
  private modesFor: string | null = null
  private readonly lastMode = new Map<string, string>()
  /** What the status line is showing: the default hint, a link problem, a dispatch error, or a dispatch result. */
  private kind: 'hint' | 'blocked' | 'error' | 'info' = 'hint'

  private static readonly HINT = 'Dispatches straight to this island\u2019s daemon.'
  private static readonly OTHER = 'other'

  constructor() {
    this.syncAgents(null)
    this.agent.addEventListener('change', () => {
      this.syncModes()
      this.syncModels()
    })
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
    this.state = s
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
    this.island = island
    const names = (island?.agents ? island.agents.filter((a) => a.available).map((a) => a.name) : [...AGENTS]).sort(byOrder)
    const key = names.join(' ')
    if (key !== this.agentsShown) {
      this.agentsShown = key
      const keep = this.agent.value
      this.agent.replaceChildren(...names.map((n) => el('option', { value: n, text: agentText(n) })))
      if (names.includes(keep)) this.agent.value = keep
    }
    this.syncModes()
    this.syncModels()
  }

  /**
   * Lists the modes the chosen agent's adapter lists, as the agent names
   * them, bringing back its last choice. The chip shows only when there
   * is a choice to make.
   */
  private syncModes(): void {
    const agent = this.agent.value
    const modes = this.island?.agents?.find((a) => a.name === agent)?.modes ?? []
    const key = JSON.stringify([agent, modes])
    if (key === this.modesShown) return
    if (this.modesFor !== null && !this.modeChip.hidden) this.lastMode.set(this.modesFor, this.mode.value)
    this.modesFor = agent
    this.modesShown = key
    this.mode.replaceChildren(...modes.map((m) => el('option', { value: m.id, text: m.name || m.id, title: m.description || null })))
    this.modeChip.hidden = modes.length < 2
    const last = this.lastMode.get(agent)
    this.mode.value = last && modes.some((m) => m.id === last) ? last : (modes[0]?.id ?? '')
  }

  /** The mode for the next session, or empty for the first the adapter lists. */
  private chosenMode(): string {
    return this.modeChip.hidden ? '' : this.mode.value
  }

  /**
   * Lists the models the chosen agent offers, bringing back its last
   * choice. Until the daemon has read them, there is the agent's default
   * and a model id typed by hand; an agent with no model setting has only
   * its default.
   */
  private syncModels(): void {
    const agent = this.agent.value
    const known = this.island?.agents?.find((a) => a.name === agent)
    const key = JSON.stringify([agent, known?.modelsRead ?? false, known?.models ?? [], known?.defaultModel ?? ''])
    if (key === this.modelsShown) return
    if (this.modelsFor !== null) this.lastModel.set(this.modelsFor, { value: this.model.value, other: this.otherModel.value })
    this.modelsFor = agent
    this.modelsShown = key
    const none = !!known?.modelsRead && !known.models?.length
    const options = [el('option', { value: '', text: known?.defaultModel ? `Agent\u2019s default (${known.defaultModel})` : 'Agent\u2019s default' })]
    if (!none) {
      options.push(...(known?.models ?? []).map((m) => el('option', { value: m.id, text: modelText(m), title: m.description || null })))
      options.push(el('option', { value: CommandBar.OTHER, text: 'Other model id\u2026' }))
    }
    this.model.replaceChildren(...options)
    this.model.disabled = none
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
      // The browser picks the session's id, which the signature names, so
      // the signed start starts this one session.
      const start = { host: this.host, session: newSessionId(), repo: this.repoId, agent: this.agent.value, mode: this.chosenMode(), model: this.chosenModel(), inCheckout: this.where.value === 'checkout', prompt }
      const signed = this.state ? signStart(this.state, start) : {}
      if (signed.error) {
        this.setWhy(signed.error, 'error')
        return
      }
      const resp = await api('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ host: start.host, sessionId: start.session, repoId: start.repo, agent: start.agent, model: start.model, mode: start.mode, prompt, inCheckout: start.inCheckout, signature: signed.signature }),
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
