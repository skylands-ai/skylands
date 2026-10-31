import * as THREE from 'three'
import { G, M, std } from './materials'

/** Who runs the agent. The hull's shape and color say which. */
export type Provider = 'claude' | 'codex' | 'gemini' | 'local'

export const PROVIDER_COLOR: Record<Provider, number> = { claude: 0xf2995e, codex: 0x3fbf9c, gemini: 0x7c9bff, local: 0xb58beb }

/** Maps an adapter name to its provider. Unknown adapters fly the local hull. */
export function providerOf(agent: string): Provider {
  if (agent.startsWith('claude')) return 'claude'
  if (agent.startsWith('codex')) return 'codex'
  if (agent.startsWith('gemini')) return 'gemini'
  return 'local'
}

export interface Airship {
  group: THREE.Group
  /** Every material the airship owns, so the whole ship can fade and be freed together. */
  mats: THREE.Material[]
  /** One sandbag per quarter of context used, hung off the gondola. */
  bags: THREE.Mesh[]
}

/** An airship: a long orange envelope for Claude, a boxy green hull for Codex, a round blue hull for Gemini, and a purple four-sided hull for local models. */
export function airshipMesh(prov: Provider): Airship {
  const g = new THREE.Group()
  const col = PROVIDER_COLOR[prov]
  const mat = std(col, { emissive: col, emissiveIntensity: 0.12 })
  // The gondola and sandbags get their own copies of the shared materials,
  // so fading one airship never dims another.
  const gondola = M.gondola.clone()
  const sand = M.sand.clone()
  let hull: THREE.Mesh
  if (prov === 'claude') {
    hull = new THREE.Mesh(new THREE.SphereGeometry(0.5, 10, 8), mat)
    hull.scale.set(1.8, 0.75, 0.75)
  } else if (prov === 'codex') {
    hull = new THREE.Mesh(G.box, mat)
    hull.scale.set(1.3, 0.55, 0.62)
  } else if (prov === 'gemini') {
    hull = new THREE.Mesh(new THREE.SphereGeometry(0.58, 10, 8), mat)
  } else {
    hull = new THREE.Mesh(new THREE.ConeGeometry(0.5, 1.3, 4), mat)
    hull.rotation.z = -Math.PI / 2
  }
  g.add(hull)
  const gond = new THREE.Mesh(G.box, gondola)
  gond.scale.set(0.45, 0.2, 0.28)
  gond.position.y = -0.55
  g.add(gond)
  const fin = new THREE.Mesh(G.box, mat)
  fin.scale.set(0.3, 0.4, 0.06)
  fin.position.set(-0.75, 0.15, 0)
  g.add(fin)
  const bags: THREE.Mesh[] = []
  for (const [x, z] of [
    [-0.32, 0.2],
    [0.32, 0.2],
    [-0.32, -0.2],
    [0.32, -0.2],
  ]) {
    const bag = new THREE.Mesh(G.bag, sand)
    bag.position.set(x, -0.6, z)
    g.add(bag)
    bags.push(bag)
  }
  return { group: g, mats: [mat, gondola, sand], bags }
}
