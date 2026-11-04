import * as THREE from 'three'
import { PROVIDER_COLOR, type Airship, type Provider } from '../../airship'
import { G, M, std } from '../../materials'

/** An airship: a long orange envelope for Claude, a boxy green hull for Codex, a round blue hull for Gemini, a taupe goose with a neck and an orange beak for goose, and a purple four-sided hull for local models. */
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
  const mats: THREE.Material[] = [mat, gondola, sand]
  let hull: THREE.Mesh
  if (prov === 'claude') {
    hull = new THREE.Mesh(new THREE.SphereGeometry(0.5, 10, 8), mat)
    hull.scale.set(1.8, 0.75, 0.75)
  } else if (prov === 'codex') {
    hull = new THREE.Mesh(G.box, mat)
    hull.scale.set(1.3, 0.55, 0.62)
  } else if (prov === 'gemini') {
    hull = new THREE.Mesh(new THREE.SphereGeometry(0.58, 10, 8), mat)
  } else if (prov === 'goose') {
    hull = new THREE.Mesh(new THREE.SphereGeometry(0.5, 10, 8), mat)
    hull.scale.set(1.5, 0.64, 0.66)
    // A neck reaching forward and up, a head, and an orange beak.
    const neck = new THREE.Mesh(new THREE.CylinderGeometry(0.07, 0.11, 0.6, 6), mat)
    neck.position.set(0.74, 0.3, 0)
    neck.rotation.z = -0.55
    const head = new THREE.Mesh(new THREE.SphereGeometry(0.15, 8, 6), mat)
    head.position.set(0.92, 0.56, 0)
    const beakMat = std(0xf0a030)
    mats.push(beakMat)
    const beak = new THREE.Mesh(new THREE.ConeGeometry(0.06, 0.22, 6), beakMat)
    beak.rotation.z = -Math.PI / 2
    beak.position.set(1.1, 0.54, 0)
    g.add(neck, head, beak)
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
  return { group: g, mats, bags }
}
