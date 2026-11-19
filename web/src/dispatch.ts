import type { Issue, IssueLink } from './issue'
import { api } from './signin'
import { agentLabel } from './status'

/**
 * Work asked for at the world or an island goes where it fits: the
 * citadel picks the island, and the island's daemon picks the repository
 * by searching its own. The browser shows where the work would go, and
 * signs the start there once the person confirms it.
 */

/** A repository an island offered for the work. */
export interface Candidate {
  host: string
  repoId: string
  name: string
  /** How much of the prompt the repository accounts for, from 0 to 1. */
  confidence: number
  /** The prompt names the repository or one of its modules. */
  named?: boolean
  /** What matched where, best first, such as "pricing in internal/pricing/". */
  reasons?: string[]
}

/** Where the citadel says the work would go, and who it asked. */
export interface Placement {
  /** Start it in target, ask host's agent to choose among, or ask the person. */
  do: 'start' | 'agent' | 'person'
  why: 'named' | 'clear' | 'close' | 'weak' | 'nothing' | 'only' | 'issue'
  target?: Candidate
  host?: string
  among?: Candidate[]
  ranked: Candidate[]
  /** The repositories in scope. */
  repos: number
  /** The islands that answered. */
  asked: string[]
  /** Islands whose repositories weren't considered: unreachable, didn't answer in time, or said why. */
  leftOut: { host: string; why: string }[]
  /** The island the prompt named at the world. */
  namedIsland?: string
  /** The issue the prompt links to, which placed the work ahead of its words. */
  issue?: IssueLink
  /** Every repository the issue is in, on any island, in scope or not, reachable or not. */
  issueRepos?: Candidate[]
}

/** What an island's agent chose: a repository, or none when it couldn't tell. */
export interface Picked {
  repoId?: string
  agent: string
}

async function post<T>(path: string, body: unknown): Promise<T | { error: string }> {
  try {
    const resp = await api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const out = (await resp.json().catch(() => ({}))) as T & { error?: string }
    if (!resp.ok) return { error: out.error ?? `The citadel answered ${resp.status}.` }
    return out
  } catch (err) {
    return { error: `Could not reach the citadel: ${(err as Error).message}` }
  }
}

/** Asks where work goes: at the island host, or at the world when null. */
export function place(prompt: string, host: string | null): Promise<Placement | { error: string }> {
  return post<Placement>('/api/dispatch', { prompt, host: host ?? '' })
}

/** Asks host's agent which of the repositories among the work is for. Its cost goes on session once it starts. */
export function pick(prompt: string, host: string, among: string[], agent: string, session: string): Promise<Picked | { error: string }> {
  return post<Picked>('/api/dispatch/pick', { prompt, host, among, agent, sessionId: session })
}

/** Reads an issue in a repository, with the gh of the island that holds it. */
export async function fetchIssue(host: string, repoId: string, url: string): Promise<Issue | { error: string }> {
  try {
    const resp = await api(`/api/repos/${encodeURIComponent(host)}/${encodeURIComponent(repoId)}/issue?url=${encodeURIComponent(url)}`)
    const out = (await resp.json().catch(() => ({}))) as Issue & { error?: string }
    if (!resp.ok) return { error: out.error ?? `The citadel answered ${resp.status}.` }
    return out
  } catch (err) {
    return { error: `Could not reach the citadel: ${(err as Error).message}` }
  }
}

/** "storefront on workstation" */
export function targetText(c: Pick<Candidate, 'name' | 'host'>): string {
  return `${c.name} on ${c.host}`
}

/** Joins names as a sentence does: "a", "a and b", "a, b, and c". */
export function joined(items: string[]): string {
  if (items.length <= 2) return items.join(' and ')
  return `${items.slice(0, -1).join(', ')}, and ${items[items.length - 1]}`
}

/** Starts a sentence with a capital. */
function capital(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Why the work goes to a candidate: "The prompt names it", "The only repository on demo", or what matched where. */
export function becauseText(c: Candidate, why?: Placement['why']): string {
  if (c.reasons?.length && (c.named || why === 'only')) return capital(c.reasons[0])
  return c.reasons?.length ? `Matched ${c.reasons.join(', ')}` : `Matched ${Math.round(c.confidence * 100)}% of the prompt`
}

/** Says which islands weren't considered and why, or empty. */
export function leftOutText(p: Pick<Placement, 'leftOut'>): string {
  if (!p.leftOut.length) return ''
  const by = (why: (w: string) => boolean): string[] => p.leftOut.filter((l) => why(l.why)).map((l) => l.host)
  const parts: string[] = []
  const away = by((w) => w === 'unreachable')
  const silent = by((w) => w === 'no answer')
  const failed = p.leftOut.filter((l) => l.why !== 'unreachable' && l.why !== 'no answer')
  if (away.length) parts.push(`${joined(away)} ${away.length === 1 ? 'is' : 'are'} unreachable`)
  if (silent.length) parts.push(`${joined(silent)} didn’t answer in time`)
  for (const f of failed) parts.push(`${f.host} said: ${f.why}`)
  return `${joined(parts)}, so ${p.leftOut.length === 1 ? 'its repositories weren’t' : 'their repositories weren’t'} considered.`
}

/** Says why the person picks the repository, at the top of the target dialog. */
export function undecidedText(p: Placement, scope: string): string {
  const among = p.among ?? []
  switch (p.why) {
    case 'named':
      if (among.length === 1) return `${among[0].name} is on ${among[0].host}, which is unreachable.`
      if (among.every((c) => c.name === among[0].name)) return `${among[0].name} is on ${joined(among.map((c) => c.host))}.`
      return `The prompt names ${joined(among.map(targetText))}.`
    case 'close':
      return `${joined(among.map(targetText))} fit about equally well.`
    case 'weak':
      return 'Nothing fits well.'
    default:
      if (!p.repos) return `No repository is registered on ${scope} yet.`
      return !p.asked.length && p.leftOut.length ? 'No island could be asked.' : 'Nothing fits.'
  }
}

/** Says the island's agent is choosing among the candidates. */
export function askingText(among: Candidate[]): string {
  return `Choosing between ${joined(among.map((c) => c.name))}…`
}

/** Says which agent chose the repository, over which others. */
export function choseText(agent: string, chosen: Candidate, among: Candidate[]): string {
  return `${agentLabel(agent)} chose it over ${joined(among.filter((c) => c.repoId !== chosen.repoId).map((c) => c.name))}`
}
