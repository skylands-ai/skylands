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

/** The room between two islands' edges on the ring. */
const ISLAND_GAP = 1.5

/** How far an island steps out from the citadel at a time while it would touch another. */
const STEP_OUT = 0.5

/** One island as the ring spaces it: where it was placed, and how big it is now. */
export interface Sized {
  host: string
  def: Placement
  /** Its radius now, which its terraces may have grown past def.R. */
  R: number
}

/**
 * How far from the citadel each island floats, once its size is known.
 * An island keeps its angle. One that grew moves out by as much as it
 * grew, so its edge stays as far from the citadel, and then further while
 * it would touch an island spaced before it, in the order of their hosts,
 * so every page spaces the same islands the same way.
 */
export function ringDistances(islands: Sized[]): Map<string, number> {
  const out = new Map<string, number>()
  const spaced: { x: number; z: number; R: number }[] = []
  for (const isl of [...islands].sort((a, b) => (a.host < b.host ? -1 : a.host > b.host ? 1 : 0))) {
    const cx = Math.cos(isl.def.angle)
    const cz = Math.sin(isl.def.angle)
    let d = isl.def.dist + Math.max(0, isl.R - isl.def.R)
    const touches = (): boolean => spaced.some((o) => Math.hypot(cx * d - o.x, cz * d - o.z) < o.R + isl.R + ISLAND_GAP)
    // Islands at almost the same angle may part only far out, so the steps stop at a limit.
    for (let steps = 0; steps < 400 && touches(); steps++) d += STEP_OUT
    spaced.push({ x: cx * d, z: cz * d, R: isl.R })
    out.set(isl.host, d)
  }
  return out
}
