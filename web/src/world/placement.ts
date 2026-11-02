import { rng } from './noise'

/** Where and how big an island is. Everything derives from the host id, so an island keeps its place. */
export interface Placement {
  angle: number
  dist: number
  y: number
  R: number
  depth: number
  seed: number
  phase: number
}

/** FNV-1a, so the same host hashes the same on every citadel and every reload. */
function hashString(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

function angleGap(a: number, b: number): number {
  let d = Math.abs(a - b) % (Math.PI * 2)
  if (d > Math.PI) d = Math.PI * 2 - d
  return d
}

/** Islands closer than this on the ring would overlap. */
const MIN_GAP = 0.6

/**
 * Places a host on the ring around the citadel. The spot comes from the host
 * id alone. When it lands on an island already there, it steps around the
 * ring until it finds room, so two hosts never share a spot. That step
 * depends on who arrived first, which is the one way a place can differ
 * between sessions.
 */
export function placeIsland(host: string, taken: Placement[]): Placement {
  const r = rng(hashString(host))
  let angle = r() * Math.PI * 2
  const p: Placement = {
    angle,
    dist: 33 + r() * 6,
    y: 4 + r() * 12,
    R: 6.5 + r() * 3.5,
    depth: 0,
    seed: Math.floor(r() * 1e6) + 1,
    phase: r() * Math.PI * 2,
  }
  p.depth = p.R + 2 + r() * 1.5
  for (let tries = 0; tries < 12 && taken.some((t) => angleGap(t.angle, angle) < MIN_GAP); tries++) {
    angle += 0.65
  }
  p.angle = angle % (Math.PI * 2)
  return p
}
