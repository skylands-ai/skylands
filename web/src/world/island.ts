import * as THREE from 'three'
import { G, M } from './materials'
import { ringNoise, rng } from './noise'
import type { Placement } from './placement'
import { crystalAt, SLAB, slabGeo, underside, type Crystal } from './shapes'

export interface IslandMeshes {
  group: THREE.Group
  /** The daemon as the citadel last heard it: lit while reachable, grey while it can't tell. */
  core: Crystal
  /** The selection ring around the island. */
  ring: THREE.Mesh
}

/**
 * One host's island: a grassy slab on a rock, with hills, trees, and maybe a
 * pond with a waterfall. Terraces for its repositories come in a later
 * milestone. Everything is seeded from the placement, so it looks the same
 * on every visit.
 */
export function buildIsland(id: string, h: Placement): IslandMeshes {
  const g = new THREE.Group()
  g.userData.host = id
  const noise = ringNoise(h.seed)
  const r = rng(h.seed * 7 + 3)
  const slab = new THREE.Mesh(slabGeo(h.R, noise), [M.soil, M.grass, M.soil])
  slab.position.y = -SLAB / 2
  g.add(slab)
  const under = new THREE.Mesh(underside(h.R * 0.97, h.depth, noise, h.seed, [0x7a5e46, 0x5a4636, 0x3e3129]), M.rock)
  under.position.y = -SLAB - h.depth / 2
  g.add(under)
  const core = crystalAt(g, -SLAB - h.depth - 1.2, 1)

  for (let k = 0; k < 3; k++) {
    const a = r() * 6.283
    const d = (0.35 + r() * 0.45) * h.R
    const hill = new THREE.Mesh(G.hill, M.hill)
    hill.scale.set(1.3 + r(), 0.45 + r() * 0.3, 1.2 + r())
    hill.position.set(Math.cos(a) * d, 0, Math.sin(a) * d)
    g.add(hill)
  }
  const WA = 1.5
  const pondR = h.R * noise(WA) * 0.62
  const pondX = Math.cos(WA) * pondR
  const pondZ = Math.sin(WA) * pondR
  let trees = Math.round(h.R * 1.8)
  const tries = trees * 4
  for (let i = 0; i < tries && trees > 0; i++) {
    const ta = r() * 6.283
    const td = Math.sqrt(r()) * 0.84 * h.R * noise(ta)
    const tx = Math.cos(ta) * td
    const tz = Math.sin(ta) * td
    if (h.waterfall && (Math.hypot(tx - pondX, tz - pondZ) < 2.3 || (Math.abs(ta - WA) < 0.3 && td > pondR * 0.8))) continue
    const tree = new THREE.Group()
    tree.add(new THREE.Mesh(G.trunk, M.trunk))
    const pine = r() < 0.55
    tree.add(new THREE.Mesh(pine ? G.pine : G.round, [M.leafA, M.leafB, M.leafC][Math.floor(r() * 3)]))
    tree.scale.setScalar(0.8 + r() * 0.6)
    tree.position.set(tx, 0, tz)
    tree.rotation.y = r() * 6.283
    g.add(tree)
    trees--
  }
  if (h.waterfall) {
    const pond = new THREE.Mesh(new THREE.CylinderGeometry(1.5, 1.5, 0.12, 14), M.pond)
    pond.position.set(pondX, 0.04, pondZ)
    g.add(pond)
    const edge = h.R * noise(WA) * 0.99
    const fall = new THREE.Mesh(new THREE.PlaneGeometry(1.1, 10), M.water)
    fall.position.set(Math.cos(WA) * (edge + 0.15), -5, Math.sin(WA) * (edge + 0.15))
    fall.rotation.y = -WA + Math.PI / 2
    g.add(fall)
    const stream = new THREE.Mesh(new THREE.BoxGeometry(edge - pondR, 0.08, 0.7), M.pond)
    stream.position.set((Math.cos(WA) * (pondR + edge)) / 2, 0.03, (Math.sin(WA) * (pondR + edge)) / 2)
    stream.rotation.y = -WA
    g.add(stream)
  }

  const ring = new THREE.Mesh(new THREE.TorusGeometry(h.R * 1.18, 0.09, 6, 96), M.ring)
  ring.rotation.x = Math.PI / 2
  ring.position.y = 0.15
  ring.visible = false
  g.add(ring)
  return { group: g, core, ring }
}
