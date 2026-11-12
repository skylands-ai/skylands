import { newSessionId, signStart } from './approvers'
import type { AgentMode, Island, PromptImage, Repo } from './protocol'
import { api } from './signin'
import { agentLabel, modelText } from './status'
import type { State } from './store'

/**
 * What a new session can start with, for the command bar and the inbox's
 * New form alike, so the two offer the same choices.
 */

/** The agents in the order a list of them goes, offered as they are until the island says which it can run. */
const AGENTS = ['fake', 'claude', 'codex', 'gemini', 'goose']

/** What the agent choice calls an agent. */
export function agentText(name: string): string {
  return name === 'fake' ? 'Fake agent, built in' : agentLabel(name)
}

/** Known agents in their usual order, then any others by name. */
function byOrder(a: string, b: string): number {
  const ia = AGENTS.indexOf(a)
  const ib = AGENTS.indexOf(b)
  if (ia !== ib) return (ia < 0 ? AGENTS.length : ia) - (ib < 0 ? AGENTS.length : ib)
  return a.localeCompare(b)
}

/** The agents a session on the island can start with: the ones its daemon found it can run, or every known one until it has checked. */
export function agentChoices(island: Island | null): string[] {
  return (island?.agents ? island.agents.filter((a) => a.available).map((a) => a.name) : [...AGENTS]).sort(byOrder)
}

/** The modes the agent's adapter lists on the island, the first being where a session starts. Fewer than two is no choice. */
export function modeChoices(island: Island | null, agent: string): AgentMode[] {
  return island?.agents?.find((a) => a.name === agent)?.modes ?? []
}

/** The model choice's value for a model id typed by hand. */
export const OTHER_MODEL = 'other'

export interface Choice {
  value: string
  text: string
  title?: string
}

/**
 * The models the agent offers on the island: its default first, then the
 * models the daemon read from it, then a model id typed by hand. Until
 * the daemon has read them there is the default and a typed id. An agent
 * with no model setting has only its default, and none is true.
 */
export function modelChoices(island: Island | null, agent: string): { options: Choice[]; none: boolean } {
  const known = island?.agents?.find((a) => a.name === agent)
  const none = !!known?.modelsRead && !known.models?.length
  const options: Choice[] = [{ value: '', text: known?.defaultModel ? `Agent’s default (${known.defaultModel})` : 'Agent’s default' }]
  if (!none) {
    options.push(...(known?.models ?? []).map((m) => ({ value: m.id, text: modelText(m), title: m.description || undefined })))
    options.push({ value: OTHER_MODEL, text: 'Other model id…' })
  }
  return { options, none }
}

/** What the model choice depends on, so it is rebuilt only when that changes. */
export function modelsKey(island: Island | null, agent: string): string {
  const known = island?.agents?.find((a) => a.name === agent)
  return JSON.stringify([agent, known?.modelsRead ?? false, known?.models ?? [], known?.defaultModel ?? ''])
}

/** Where a session works: its own worktree, or the registered checkout itself. */
export const WHERE_CHOICES: Choice[] = [
  { value: 'worktree', text: 'A worktree of its own' },
  { value: 'checkout', text: 'The checkout itself' },
]

/** The repositories registered across the world, by island: islands by host, each one's repositories by name. */
export function repoChoices(s: Pick<State, 'repos'>): { host: string; repos: Repo[] }[] {
  const byHost = new Map<string, Repo[]>()
  for (const r of Object.values(s.repos)) byHost.set(r.host, [...(byHost.get(r.host) ?? []), r])
  return [...byHost.keys()].sort((a, b) => a.localeCompare(b)).map((host) => ({ host, repos: (byHost.get(host) ?? []).sort((a, b) => a.name.localeCompare(b.name)) }))
}

/** A session to start: where, with what, and its first prompt. */
export interface Start {
  host: string
  repo: string
  agent: string
  /** Empty for the first mode the adapter lists. */
  mode: string
  /** Empty for the agent's default. */
  model: string
  inCheckout: boolean
  prompt: string
  /** The files the prompt mentions and the images uploaded for it, which its signature covers. */
  mentions?: string[]
  images?: PromptImage[]
}

/**
 * Why a prompt to the agent on the island can't carry images, or empty
 * when it can: the agent's daemon read whether it takes them when it last
 * checked, and checks again when the prompt arrives.
 */
export function imagesRefused(island: Island | null | undefined, agent: string): string {
  const known = island?.agents?.find((a) => a.name === agent)
  if (!island || !known) return `${island?.host ?? 'The island'} hasn’t said yet whether ${agentLabel(agent)} takes images.`
  return known.images ? '' : `${agentLabel(agent)} doesn’t take images.`
}

/**
 * Starts a session on the island's daemon. The browser picks the session's
 * id, which the signature names, so the signed start starts this one
 * session. Resolves with the id, or with what went wrong.
 */
export async function startSession(s: State, start: Start): Promise<{ session: string; error?: undefined } | { error: string }> {
  const session = newSessionId()
  const signed = signStart(s, { ...start, session })
  if (signed.error) return { error: signed.error }
  try {
    const resp = await api('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host: start.host, sessionId: session, repoId: start.repo, agent: start.agent, model: start.model, mode: start.mode, prompt: start.prompt, mentions: start.mentions ?? [], images: start.images ?? [], inCheckout: start.inCheckout, signature: signed.signature }),
    })
    const body = (await resp.json().catch(() => ({}))) as { error?: string }
    if (!resp.ok) return { error: body.error ?? `The citadel answered ${resp.status}.` }
    return { session }
  } catch (err) {
    return { error: `Could not reach the citadel: ${(err as Error).message}` }
  }
}
