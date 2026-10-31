import type { Island, Session } from './protocol'
import type { State } from './store'
import type { Status } from './world/world'

/** True when this page can't reach the citadel, so everything shown is the last state heard. */
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
  if (stale) return ['Last state heard', 'var(--idle)']
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
    default:
      return '#B58BEB'
  }
}

/** What a session is doing right now, in a few words. */
export function sessionStatus(s: Session): Status {
  if (s.status === 'failed') return [`Failed: ${s.error ?? 'unknown error'}`, 'var(--bad)']
  if (s.status === 'ended') return [`Finished${s.stopReason && s.stopReason !== 'end_turn' ? ` (${s.stopReason.replace(/_/g, ' ')})` : ''}`, 'var(--muted)']
  return [s.currentAction ?? (s.agentName ? 'Thinking' : 'Starting'), 'var(--ok)']
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
