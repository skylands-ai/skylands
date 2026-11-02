import * as THREE from 'three'
import { G, M } from './materials'
import { ringNoise, rng } from './noise'
import type { Placement } from './placement'
import { crystalAt, selectionRing, SLAB, slabGeo, underside, type Crystal } from './shapes'

export interface IslandMeshes {
  group: THREE.Group
  /** The daemon as the citadel last heard it: lit while reachable, grey while it can't tell. */
  core: Crystal
  /** The selection ring around the island. It turns slowly while visible. */
  ring: THREE.Group
}

/**
 * One host's island: a grassy slab on a rock, with hills and trees.
 * Everything is seeded from the placement, so it looks the same on
 * every visit.
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
    hill.userData.scenery = 'hill'
    g.add(hill)
  }
  let trees = Math.round(h.R * 1.8)
  const tries = trees * 4
  for (let i = 0; i < tries && trees > 0; i++) {
    const ta = r() * 6.283
    const td = Math.sqrt(r()) * 0.84 * h.R * noise(ta)
    const tx = Math.cos(ta) * td
    const tz = Math.sin(ta) * td
    const tree = new THREE.Group()
    tree.add(new THREE.Mesh(G.trunk, M.trunk))
    const pine = r() < 0.55
    tree.add(new THREE.Mesh(pine ? G.pine : G.round, [M.leafA, M.leafB, M.leafC][Math.floor(r() * 3)]))
    tree.scale.setScalar(0.8 + r() * 0.6)
    tree.position.set(tx, 0, tz)
    tree.rotation.y = r() * 6.283
    tree.userData.scenery = 'tree'
    g.add(tree)
    trees--
  }
  const ring = selectionRing(h.R * 1.18, 0.09)
  ring.position.y = 0.15
  g.add(ring)
  return { group: g, core, ring }
}
