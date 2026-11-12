import type { AgentModel, Island, Session, TokenUsage } from './protocol'
import type { State } from './store'
import type { Status } from './world/world'

/** True when this page can't reach the citadel, so everything shown is the last state seen. */
export function isStale(s: State): boolean {
  return s.link !== 'connected'
}

/** The citadel's health as this browser sees it. */
export function citadelStatus(s: State): Status {
  if (s.link === 'connecting') return ['Connecting', 'var(--warn)']
  if (s.link === 'lost') return ['Unreachable', 'var(--bad)']
  if (!s.server?.connected) return ['Waiting for NATS', 'var(--warn)']
  return ['Healthy', 'var(--ok)']
}

/** How many islands the citadel can reach, and how many it knows. */
export function islandCounts(s: State): { reachable: number; total: number } {
  const all = Object.values(s.islands)
  return { reachable: all.filter((i) => i.reachable).length, total: all.length }
}

/** What the citadel's floating label says under its name. */
export function citadelLabelStatus(s: State): Status {
  if (s.link !== 'connected' || !s.server?.connected) return citadelStatus(s)
  const { reachable, total } = islandCounts(s)
  return [total ? `${reachable} of ${total} islands connected` : 'No islands yet', 'var(--ok)']
}

/** An island's link as the citadel sees it, or its last state when this page can't reach the citadel. */
export function islandStatus(i: Island, stale: boolean): Status {
  if (stale) return ['Last state seen', 'var(--idle)']
  return i.reachable ? ['Connected', 'var(--ok)'] : ['Unreachable', 'var(--idle)']
}

/** "12:37:05" in the viewer's locale. */
export function clockText(ms: number): string {
  return new Date(ms).toLocaleTimeString()
}

/** The provider color for an agent, matching the airship hulls. */
export function agentColor(agent: string): string {
  switch (agent) {
    case 'claude':
      return '#F2995E'
    case 'codex':
      return '#3FBF9C'
    case 'gemini':
      return '#7C9BFF'
    case 'goose':
      return '#9C8466'
    default:
      return '#B58BEB'
  }
}

/**
 * A model as a chip lists it: its id, which is what a session is set to,
 * followed by the agent's name for it when that name says more.
 */
export function modelText(m: AgentModel): string {
  const squash = (s: string) => s.toLowerCase().replace(/[^a-z0-9]/g, '')
  return m.name && !squash(m.id).includes(squash(m.name)) ? `${m.id} \u00b7 ${m.name}` : m.id
}

/** What to call a session's mode: the agent's name for it, as its island last read it, or its id. */
export function modeName(island: Island | undefined, s: Session): string {
  const mode = island?.agents?.find((a) => a.name === s.agent)?.modes?.find((m) => m.id === s.mode)
  return mode?.name || s.mode || ''
}

/** What to call an adapter in the interface. */
export function agentLabel(agent: string): string {
  switch (agent) {
    case 'claude':
      return 'Claude Code'
    case 'codex':
      return 'Codex'
    case 'gemini':
      return 'Gemini CLI'
    case 'goose':
      return 'goose'
    case 'fake':
      return 'Fake agent'
    default:
      return agent
  }
}

/**
 * A conversation's label: the title its agent gave it, or until then the
 * first line of its first prompt, cut to fit a subject line.
 */
export function sessionLabel(s: Session, max = 72): string {
  const line = s.title || (s.prompt.split('\n').find((l) => l.trim() !== '') ?? '')
  const text = line.replace(/[#*_`>]+/g, '').replace(/\s+/g, ' ').trim() || 'Untitled conversation'
  return text.length > max ? text.slice(0, max - 1).trimEnd() + '\u2026' : text
}

/** The yellow of "waiting on you", shared with the tags and the beacon. */
export const WAITING = '#E0B000'

/**
 * What a session is doing right now, in a few words. An idle session is
 * "waiting for you" until its last message has been seen, then just idle.
 */
export function sessionStatus(s: Session, waiting = s.status === 'idle'): Status {
  if (s.status === 'failed') return [`Failed: ${s.error ?? 'unknown error'}`, 'var(--bad)']
  if (s.status === 'ended') return [`Finished${s.stopReason && s.stopReason !== 'end_turn' && s.stopReason !== 'stopped' ? ` (${s.stopReason.replace(/_/g, ' ')})` : s.stopReason === 'stopped' ? ', stopped' : ''}`, 'var(--muted)']
  if (s.pending.length) return [`Needs your approval: ${s.pending[0].title}`, WAITING]
  if (s.questions.length) return [`Asks you: ${s.questions[0].message}`, WAITING]
  if (s.status === 'idle') return waiting ? [s.stopReason === 'cancelled' ? 'Interrupted, waiting for you' : 'Waiting for you', WAITING] : ['Idle', 'var(--idle)']
  if (s.cancelling) return ['Stopping\u2026', 'var(--idle)']
  return [s.currentAction ?? (s.agentName ? 'Thinking' : 'Starting'), 'var(--ok)']
}

/** Whether the session is still open: an agent process exists for it. */
export function isOpen(s: Session): boolean {
  return s.status === 'running' || s.status === 'idle'
}

/** Sessions that are over and still have a worktree of their own, which removing cleans up. */
export function leftBehind(sessions: Session[]): Session[] {
  return sessions.filter((s) => !isOpen(s) && !s.inCheckout && !s.worktreeRemoved)
}

/** Shortens a NATS id to its ends, the way the prototype shows them. */
export function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 5)}…${id.slice(-5)}` : id
}

/** "12 s", "3 min", "2 hours", "4 days". */
export function durationText(sec: number): string {
  sec = Math.max(0, Math.floor(sec))
  if (sec < 60) return `${sec} s`
  if (sec < 3600) return `${Math.floor(sec / 60)} min`
  const hrs = Math.floor(sec / 3600)
  if (hrs < 48) return `${hrs} ${hrs === 1 ? 'hour' : 'hours'}`
  return `${Math.floor(hrs / 24)} days`
}

/** Seconds since an ISO string or ms since the epoch. */
export function secondsSince(when: string | number, now = Date.now()): number {
  const then = typeof when === 'number' ? when : Date.parse(when)
  return (now - then) / 1000
}

/** "12 s ago", "3 min ago", "2 hours ago". */
export function agoText(when: string | number, now = Date.now()): string {
  return `${durationText(secondsSince(when, now))} ago`
}

/** "up 2 hours", "up 3 days". */
export function upText(iso: string, now = Date.now()): string {
  const sec = secondsSince(iso, now)
  return sec < 60 ? 'up under a minute' : `up ${durationText(sec)}`
}

/** A token count short enough for a fact or a header: "843", "12.3k", "123k", "1.23M", "12.3M". */
export function tokensText(n: number): string {
  if (n < 1000) return String(n)
  if (n < 100_000) return `${(n / 1000).toFixed(1)}k`
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`
  if (n < 10_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  return `${(n / 1_000_000).toFixed(1)}M`
}

/**
 * "12.3k tokens", or "no tokens yet" before any request has been counted.
 * While a turn runs, "12.3k tokens + this turn": agents report a turn's
 * tokens only as it ends, so until then the total is the earlier turns' and
 * the title request's.
 */
export function tokensLabel(t: TokenUsage, running = false): string {
  if (!t.total) return 'no tokens yet'
  return `${tokensText(t.total)} tokens${running ? ' + this turn' : ''}`
}

/** What a total leaves out while a turn runs. */
const TURN_UNCOUNTED = 'The turn in progress is counted when it ends.'

/**
 * The exact counts behind a token total, for a tooltip: "12,345 tokens:
 * 10,000 in, 2,000 out, 345 cache read". While a turn runs it adds that the
 * turn is not in them yet. Empty when nothing was counted and no turn runs.
 */
export function tokensBreakdown(t: TokenUsage, running = false): string {
  if (!t.total) return running ? TURN_UNCOUNTED : ''
  const parts = [`${t.input.toLocaleString()} in`, `${t.output.toLocaleString()} out`]
  if (t.cachedRead) parts.push(`${t.cachedRead.toLocaleString()} cache read`)
  if (t.cachedWrite) parts.push(`${t.cachedWrite.toLocaleString()} cache write`)
  if (t.thought) parts.push(`${t.thought.toLocaleString()} thought`)
  const counts = `${t.total.toLocaleString()} tokens: ${parts.join(', ')}`
  return running ? `${counts}. ${TURN_UNCOUNTED}` : counts
}
