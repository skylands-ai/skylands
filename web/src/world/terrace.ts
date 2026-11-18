import type * as THREE from 'three'
import type { Repo, TerrainFile } from '../protocol'
import { rng } from './noise'
import type { BuildingMats, TerraceMeshes, WorldTheme } from './theme'

/** Buildings drawn per terrace at most: an 8 by 8 grid. The daemon already keeps only this many files, largest first. */
export const MAX_BUILDINGS = 64

/** Where a terrace sits on its island and how big it is. */
export interface TerraceSpec {
  id: string
  name: string
  x: number
  z: number
  sx: number
  sz: number
  /** The grid of building lots on the plaza, which sets its size. */
  cols: number
  rows: number
  /** The files that get buildings, largest first when capped. */
  files: TerrainFile[]
  /** How many tracked files the repo has in all. */
  total: number
}

/** One color per repo, the same everywhere the repo appears: its terrace's boundary in the world. */
const REPO_COLORS = [0xc8553d, 0x3f7cc4, 0x3fa37c, 0x9b6bc8, 0xd9a23b, 0x2e9e57]

function hashId(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

export function ropeColor(id: string): number {
  return REPO_COLORS[hashId(id) % REPO_COLORS.length]
}

const CELL = 0.95

/** The height of a terrace's plaza, which its buildings stand on. */
export const PLAZA_TOP = 0.35

/** The room between two terraces' slots, which their boundary posts stand in. */
const TERRACE_GAP = 1

/** How far a terrace's boundary posts stand out from its plaza, in any theme, and a little more. */
const POSTS = 0.3

/**
 * The share of an island's radius a terrace's corners may reach. An
 * island's edge wobbles in and out by up to about a sixth of its radius,
 * and a terrace's corner can be where it dips.
 */
const EDGE = 0.8

/** The side of a terrace's slot on its island: a plaza of the most buildings a terrace shows, 8 by 8. */
const SLOT = Math.sqrt(MAX_BUILDINGS) * CELL + 0.5

/** An island's terraces, laid out, and the radius its top needs to hold them. */
export interface IslandLayout {
  /** The island's own radius, or more when its terraces need more. */
  R: number
  terraces: TerraceSpec[]
}

/**
 * Lays out an island's terraces: one in the middle, or rows of them, as
 * square as they go. Each terrace has a slot of the same size, room for 8
 * by 8 buildings, with its plaza, sized by how many files it shows, in the
 * middle of it. The island grows to hold every slot: the layout's R is R,
 * or more when the slots' corners would reach past the island's edge, so
 * how big an island is depends on how many terraces it holds and nothing
 * else.
 */
export function layoutIsland(R: number, repos: Repo[]): IslandLayout {
  if (!repos.length) return { R, terraces: [] }
  const slots = bestRows(repos.length)
  const terraces = repos.map((repo, k): TerraceSpec => {
    const files = [...repo.files].sort((a, b) => b.size - a.size).slice(0, MAX_BUILDINGS)
    // A square grid, so 64 files make 8 by 8.
    const cols = Math.max(1, Math.ceil(Math.sqrt(files.length)))
    const rows = Math.max(1, Math.ceil(files.length / cols))
    const [x, z] = slots.centers[k]
    return { id: repo.id, name: repo.name, x, z, sx: cols * CELL + 0.5, sz: rows * CELL + 0.5, cols, rows, files: files.slice(0, cols * rows), total: repo.totalFiles }
  })
  return { R: Math.max(R, slots.reach / EDGE), terraces }
}

/**
 * The centers of n slots in rows, each row centered and the rows centered
 * about the island's middle, and how far their corners reach, with room
 * for posts. Of every way to put them in rows, it is the one the smallest
 * island holds, and of those the widest, since the island is seen from
 * the side.
 */
function bestRows(n: number): { centers: [number, number][]; reach: number } {
  const pitch = SLOT + TERRACE_GAP
  let best: { centers: [number, number][]; reach: number } | null = null
  for (let across = n; across >= 1; across--) {
    const rows = Math.ceil(n / across)
    const centers: [number, number][] = []
    for (let k = 0; k < n; k++) {
      const row = Math.floor(k / across)
      const inRow = Math.min(across, n - row * across)
      centers.push([((k % across) - (inRow - 1) / 2) * pitch, (row - (rows - 1) / 2) * pitch])
    }
    const reach = Math.max(...centers.map(([x, z]) => Math.hypot(Math.abs(x) + SLOT / 2 + POSTS, Math.abs(z) + SLOT / 2 + POSTS)))
    if (!best || reach < best.reach - 1e-9) best = { centers, reach }
  }
  return best as { centers: [number, number][]; reach: number }
}

/** Where one file's building stands, from its terrace's center, and how tall it is. */
export interface Lot {
  x: number
  z: number
  height: number
  file: TerrainFile
  idx: number
}

/**
 * The lots of a terrace's buildings: one per file on a seeded shuffle of
 * the grid, each as tall as the square root of its file's share of the
 * largest. The world stands every theme's buildings on these, so an
 * airship lands on the same roof whichever theme draws it.
 */
export function terraceLots(t: TerraceSpec, seed: number): Lot[] {
  const r = rng(seed)
  // The grid comes from the layout, not from the plaza's size: 3 * 0.95
  // divided by 0.95 is a hair under 3, which would lose a row of lots.
  const cells: [number, number][] = []
  for (let cx = 0; cx < t.cols; cx++) for (let cz = 0; cz < t.rows; cz++) cells.push([cx, cz])
  for (let s = cells.length - 1; s > 0; s--) {
    const q = Math.floor(r() * (s + 1))
    const tmp = cells[s]
    cells[s] = cells[q]
    cells[q] = tmp
  }
  const maxSize = Math.max(1, ...t.files.map((f) => f.size))
  return cells.slice(0, Math.min(t.files.length, cells.length)).map((c, idx) => {
    const file = t.files[idx]
    return {
      x: (c[0] + 0.5 - t.cols / 2) * CELL,
      z: (c[1] + 0.5 - t.rows / 2) * CELL,
      height: 0.4 + 2.2 * Math.sqrt(file.size / maxSize),
      file,
      idx,
    }
  })
}

/**
 * Makes a mesh a terrace's building: stands it on its lot, in its theme's
 * resting material, with what the world reads from it. Its geometry is a
 * unit block, stretched here to the lot's footprint and height.
 */
function asBuilding(b: THREE.Mesh, lot: Lot, t: TerraceSpec, mats: BuildingMats): void {
  b.material = mats.rest
  b.scale.set(0.7, lot.height, 0.7)
  b.position.set(lot.x, PLAZA_TOP + lot.height / 2, lot.z)
  b.userData = { top: PLAZA_TOP + lot.height, file: lot.file.path, size: lot.file.size, terraceId: t.id, bldIdx: lot.idx, dwell: {}, mats }
}

/**
 * A terrace in a theme, at its place on its island: the theme's plaza and
 * boundary, with the theme's building for each file standing on its lot.
 * Anything a click lands on in it names the terrace, and a building names
 * its file too.
 */
export function placeTerrace(theme: WorldTheme, t: TerraceSpec, seed: number): { meshes: TerraceMeshes; blds: THREE.Mesh[] } {
  const meshes = theme.buildTerrace({ sx: t.sx, sz: t.sz, color: ropeColor(t.id) })
  const g = meshes.group
  g.position.set(t.x, 0, t.z)
  g.userData.terraceId = t.id
  const blds = terraceLots(t, seed).map((lot) => {
    const b = theme.building(lot.height)
    asBuilding(b, lot, t, theme.buildings)
    g.add(b)
    return b
  })
  return { meshes, blds }
}

/** Points around a terrace's plaza, from its center, out from its edge and about a step apart, where its boundary posts stand. */
export function boundaryPoints(t: { sx: number; sz: number }, out: number, step: number): [number, number][] {
  const hx = t.sx / 2 + out
  const hz = t.sz / 2 + out
  const corners = [
    [-hx, -hz],
    [hx, -hz],
    [hx, hz],
    [-hx, hz],
  ]
  const pts: [number, number][] = []
  for (let c = 0; c < 4; c++) {
    const a = corners[c]
    const b = corners[(c + 1) % 4]
    const n = Math.max(1, Math.round(Math.hypot(b[0] - a[0], b[1] - a[1]) / step))
    for (let i = 0; i < n; i++) pts.push([a[0] + ((b[0] - a[0]) * i) / n, a[1] + ((b[1] - a[1]) * i) / n])
  }
  return pts
}
