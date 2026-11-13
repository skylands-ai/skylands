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

/**
 * Lays out an island's terraces: one in the middle, or a ring of them, each
 * sized by how many files it shows and clamped to fit the island.
 */
export function layoutTerraces(R: number, repos: Repo[]): TerraceSpec[] {
  const n = repos.length
  const maxSide = n === 1 ? R * 1.15 : R * 0.8
  const maxCells = Math.floor((maxSide - 0.5) / CELL)
  return repos.map((repo, k) => {
    const files = [...repo.files].sort((a, b) => b.size - a.size).slice(0, Math.min(MAX_BUILDINGS, maxCells * maxCells))
    // A square grid, so 64 files make 8 by 8.
    const cols = Math.max(1, Math.min(maxCells, Math.ceil(Math.sqrt(files.length))))
    const rows = Math.max(1, Math.min(maxCells, Math.ceil(files.length / cols)))
    const sx = cols * CELL + 0.5
    const sz = rows * CELL + 0.5
    let x = 0
    let z = 0
    if (n > 1) {
      const a = (k / n) * Math.PI * 2 + 0.6
      const d = R * 0.42
      x = Math.cos(a) * d
      z = Math.sin(a) * d
    }
    return { id: repo.id, name: repo.name, x, z, sx, sz, cols, rows, files: files.slice(0, cols * rows), total: repo.totalFiles }
  })
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
