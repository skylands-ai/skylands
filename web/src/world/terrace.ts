import * as THREE from 'three'
import type { Repo, TerrainFile } from '../protocol'
import { G, M, std } from './materials'
import { rng } from './noise'

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
  /** The files that get buildings, largest first when capped. */
  files: TerrainFile[]
  /** How many tracked files the repo has in all. */
  total: number
}

export interface TerraceMeshes {
  group: THREE.Group
  blds: THREE.Mesh[]
  /** The rope's material, which pulses while the terrace is selected. */
  rope: THREE.MeshStandardMaterial
}

/** The rope's resting glow. */
export const ROPE_GLOW = 0.25

/** One rope color per repo, the same everywhere the repo appears. */
const ROPE_COLORS = [0xc8553d, 0x3f7cc4, 0x3fa37c, 0x9b6bc8, 0xd9a23b, 0x2e9e57]

function hashId(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

export function ropeColor(id: string): number {
  return ROPE_COLORS[hashId(id) % ROPE_COLORS.length]
}

const CELL = 0.95

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
    return { id: repo.id, name: repo.name, x, z, sx, sz, files: files.slice(0, cols * rows), total: repo.totalFiles }
  })
}

const POST_MAT = std(0xb8954a, { metalness: 0.45, roughness: 0.45 })
const POST_GEO = new THREE.CylinderGeometry(0.045, 0.06, 0.6, 6)
const KNOB_GEO = new THREE.SphereGeometry(0.08, 8, 6)
const FOOT_GEO = new THREE.CylinderGeometry(0.12, 0.14, 0.05, 8)

/** Stanchions with a colored rope around a terrace: the repository boundary. Returns the rope's material. */
function addStanchions(g: THREE.Group, t: TerraceSpec): THREE.MeshStandardMaterial {
  const col = ropeColor(t.id)
  const ropeMat = new THREE.MeshStandardMaterial({ color: col, emissive: col, emissiveIntensity: ROPE_GLOW, roughness: 0.8 })
  const hx = t.sx / 2 + 0.14
  const hz = t.sz / 2 + 0.14
  const base = 0.35
  const top = base + 0.6
  const anchor = top - 0.07
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
    const n = Math.max(1, Math.round(Math.hypot(b[0] - a[0], b[1] - a[1]) / 1.1))
    for (let i = 0; i < n; i++) pts.push([t.x + a[0] + ((b[0] - a[0]) * i) / n, t.z + a[1] + ((b[1] - a[1]) * i) / n])
  }
  pts.forEach((p, i) => {
    const foot = new THREE.Mesh(FOOT_GEO, POST_MAT)
    foot.position.set(p[0], base + 0.025, p[1])
    const post = new THREE.Mesh(POST_GEO, POST_MAT)
    post.position.set(p[0], base + 0.3, p[1])
    const knob = new THREE.Mesh(KNOB_GEO, POST_MAT)
    knob.position.set(p[0], top + 0.04, p[1])
    const q = pts[(i + 1) % pts.length]
    const curve = new THREE.QuadraticBezierCurve3(
      new THREE.Vector3(p[0], anchor, p[1]),
      new THREE.Vector3((p[0] + q[0]) / 2, anchor - 0.32, (p[1] + q[1]) / 2),
      new THREE.Vector3(q[0], anchor, q[1]),
    )
    const rope = new THREE.Mesh(new THREE.TubeGeometry(curve, 10, 0.035, 5, false), ropeMat)
    for (const m of [foot, post, knob, rope]) {
      m.userData.terraceId = t.id
      g.add(m)
    }
  })
  return ropeMat
}

/**
 * A terrace: a plaza with a rope around it and a building per file, its
 * height scaled by file size. Building placement is seeded so a repo looks
 * the same on every visit.
 */
export function buildTerrace(t: TerraceSpec, seed: number): TerraceMeshes {
  const g = new THREE.Group()
  const r = rng(seed)
  const plaza = new THREE.Mesh(G.box, M.plaza)
  plaza.scale.set(t.sx, 0.35, t.sz)
  plaza.position.set(t.x, 0.17, t.z)
  plaza.userData.terraceId = t.id
  g.add(plaza)
  const rope = addStanchions(g, t)

  const cols = Math.max(1, Math.floor((t.sx - 0.5) / CELL))
  const rows = Math.max(1, Math.floor((t.sz - 0.5) / CELL))
  const cells: [number, number][] = []
  for (let cx = 0; cx < cols; cx++) for (let cz = 0; cz < rows; cz++) cells.push([cx, cz])
  for (let s = cells.length - 1; s > 0; s--) {
    const q = Math.floor(r() * (s + 1))
    const tmp = cells[s]
    cells[s] = cells[q]
    cells[q] = tmp
  }
  const maxSize = Math.max(1, ...t.files.map((f) => f.size))
  const blds: THREE.Mesh[] = []
  cells.slice(0, Math.min(t.files.length, cells.length)).forEach((c, idx) => {
    const f = t.files[idx]
    const bh = 0.4 + 2.2 * Math.sqrt(f.size / maxSize)
    const b = new THREE.Mesh(G.box, M.bld)
    b.scale.set(0.7, bh, 0.7)
    const bx = t.x - t.sx / 2 + 0.25 + (c[0] + 0.5) * ((t.sx - 0.5) / cols)
    const bz = t.z - t.sz / 2 + 0.25 + (c[1] + 0.5) * ((t.sz - 0.5) / rows)
    b.position.set(bx, 0.35 + bh / 2, bz)
    b.userData = { top: 0.35 + bh, file: f.path, size: f.size, terraceId: t.id, bldIdx: idx, dwell: {} }
    // Outline every edge, so buildings keep their individuality at low angles.
    // The outline sits a hair outside the faces, so no renderer can hide it.
    const edges = new THREE.LineSegments(G.boxEdges, M.bldEdge)
    edges.scale.setScalar(1.006)
    // Outlines are not pickable. A line counts as hit anywhere near the ray,
    // so a neighbor's edge would steal clicks meant for this roof.
    edges.raycast = () => {}
    b.add(edges)
    g.add(b)
    blds.push(b)
  })
  return { group: g, blds, rope }
}

/** Frees a terrace's own geometry. Shared geometry and materials stay. */
export function disposeTerrace(t: TerraceMeshes): void {
  t.group.traverse((o) => {
    if (o instanceof THREE.Mesh && o.geometry !== G.box && o.geometry !== POST_GEO && o.geometry !== KNOB_GEO && o.geometry !== FOOT_GEO) {
      o.geometry.dispose()
    }
  })
  t.rope.dispose()
  t.group.parent?.remove(t.group)
}
