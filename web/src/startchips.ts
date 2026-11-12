import { el } from './dom'
import type { Island } from './protocol'
import { agentChoices, agentText, modeChoices, modelChoices, modelsKey, OTHER_MODEL, WHERE_CHOICES } from './start'

/** The controls a new session's agent, mode, model, and Where are chosen with. */
export interface StartControls {
  agent: HTMLSelectElement
  /** Holds the mode choice, which shows only for an agent with more than one mode. */
  modeChip: HTMLElement
  mode: HTMLSelectElement
  model: HTMLSelectElement
  /** A model id typed by hand, shown while the model is "Other". */
  otherModel: HTMLInputElement
  where: HTMLSelectElement
}

/** What a new session starts with, as the chips show it. */
export interface Chosen {
  agent: string
  /** Empty for the first mode the adapter lists. */
  mode: string
  /** Empty for the agent's default. */
  model: string
  inCheckout: boolean
}

/**
 * The agent, mode, model, and Where chips of a new session, filled from
 * what the island says it can run. The command bar and the inbox's New
 * form each have a set. The agent stays chosen while the island lists
 * it, and each agent's last mode and model come back when it is chosen
 * again.
 */
export class StartChips {
  /** The island whose agents the chips list. */
  private island: Island | null = null
  /** The agents the agent chip lists, so it is rebuilt only when they change. */
  private agentsShown: string | null = null
  /** The agent and models the model chip lists, and each agent's last model choice. */
  private modelsShown: string | null = null
  private modelsFor: string | null = null
  private readonly lastModel = new Map<string, { value: string; other: string }>()
  /** The agent and modes the mode chip lists, and each agent's last mode choice. */
  private modesShown: string | null = null
  private modesFor: string | null = null
  private readonly lastMode = new Map<string, string>()

  constructor(private readonly c: StartControls) {
    c.where.replaceChildren(...WHERE_CHOICES.map((w) => el('option', { value: w.value, text: w.text })))
    c.agent.addEventListener('change', () => {
      this.syncModes()
      this.syncModels()
    })
    c.model.addEventListener('change', () => {
      const other = c.model.value === OTHER_MODEL
      c.otherModel.hidden = !other
      if (other) c.otherModel.focus()
    })
    this.show(null)
  }

  /** Lists what the island can run, or every known agent until its daemon has said. */
  show(island: Island | null): void {
    this.island = island
    const names = agentChoices(island)
    const key = names.join(' ')
    if (key !== this.agentsShown) {
      this.agentsShown = key
      const keep = this.c.agent.value
      this.c.agent.replaceChildren(...names.map((n) => el('option', { value: n, text: agentText(n) })))
      if (names.includes(keep)) this.c.agent.value = keep
    }
    this.syncModes()
    this.syncModels()
  }

  /** Whether the island can run any agent at all. */
  hasAgents(): boolean {
    return this.c.agent.options.length > 0
  }

  chosen(): Chosen {
    const model = this.c.model.value === OTHER_MODEL ? this.c.otherModel.value.trim() : this.c.model.value
    return { agent: this.c.agent.value, mode: this.c.modeChip.hidden ? '' : this.c.mode.value, model, inCheckout: this.c.where.value === 'checkout' }
  }

  /** Lists the chosen agent's modes as the agent names them, bringing back its last choice. */
  private syncModes(): void {
    const agent = this.c.agent.value
    const modes = modeChoices(this.island, agent)
    const key = JSON.stringify([agent, modes])
    if (key === this.modesShown) return
    if (this.modesFor !== null && !this.c.modeChip.hidden) this.lastMode.set(this.modesFor, this.c.mode.value)
    this.modesFor = agent
    this.modesShown = key
    this.c.mode.replaceChildren(...modes.map((m) => el('option', { value: m.id, text: m.name || m.id, title: m.description || null })))
    this.c.modeChip.hidden = modes.length < 2
    const last = this.lastMode.get(agent)
    this.c.mode.value = last && modes.some((m) => m.id === last) ? last : (modes[0]?.id ?? '')
  }

  /** Lists the chosen agent's models, bringing back its last choice. */
  private syncModels(): void {
    const agent = this.c.agent.value
    const key = modelsKey(this.island, agent)
    if (key === this.modelsShown) return
    if (this.modelsFor !== null) this.lastModel.set(this.modelsFor, { value: this.c.model.value, other: this.c.otherModel.value })
    this.modelsFor = agent
    this.modelsShown = key
    const { options, none } = modelChoices(this.island, agent)
    this.c.model.replaceChildren(...options.map((o) => el('option', { value: o.value, text: o.text, title: o.title ?? null })))
    this.c.model.disabled = none
    const last = this.lastModel.get(agent)
    this.c.model.value = last && options.some((o) => o.value === last.value) ? last.value : ''
    this.c.otherModel.value = last?.other ?? ''
    this.c.otherModel.hidden = this.c.model.value !== OTHER_MODEL
  }
}
