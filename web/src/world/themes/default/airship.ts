import * as THREE from 'three'
import { PROVIDER_COLOR, type Airship, type Provider } from '../../airship'
import { G, M, std } from '../../materials'

/** An airship: a long orange envelope for Claude, a boxy green hull for Codex, a round blue hull for Gemini, and a purple four-sided hull for local models. */
export function airshipMesh(prov: Provider): Airship {
  const g = new THREE.Group()
  const col = PROVIDER_COLOR[prov]
  const mat = std(col, { emissive: col, emissiveIntensity: 0.12 })
  // The gondola and sandbags get their own copies of the shared materials,
  // so fading one airship never dims another.
  const gondola = M.gondola.clone()
  const sand = M.sand.clone()
  gondola.userData = {}
  sand.userData = {}
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
