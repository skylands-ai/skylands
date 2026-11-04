import * as THREE from 'three'
import { PROVIDER_COLOR, type Airship, type Provider } from '../../airship'
import { G, std } from '../../materials'

/** Places a mesh in a ship: at a position, turned, and stretched. */
function part(g: THREE.Object3D, geo: THREE.BufferGeometry, mat: THREE.Material, pos: [number, number, number], rot: [number, number, number] = [0, 0, 0], scale: [number, number, number] = [1, 1, 1]): THREE.Mesh {
  const m = new THREE.Mesh(geo, mat)
  m.position.set(...pos)
  m.rotation.set(...rot)
  m.scale.set(...scale)
  g.add(m)
  return m
}

/**
 * A ship in the sci-fi theme, flying nose first along +x: a shuttle for
 * Claude, a hauler for Codex, a saucer for Gemini, a swept-wing glider for
 * goose, and a ringed drone for local models, each in its provider's color. Four lit cells under the
 * hull count the context used, a quarter each.
 */
export function starship(prov: Provider): Airship {
  const g = new THREE.Group()
  const col = PROVIDER_COLOR[prov]
  // Every material is the ship's own, so fading one ship never dims another.
  const hull = std(col, { emissive: col, emissiveIntensity: 0.15, metalness: 0.35, roughness: 0.45 })
  const dark = std(0x3a414c, { metalness: 0.5, roughness: 0.4 })
  const engine = new THREE.MeshBasicMaterial({ color: 0xbff4ff })
  const cellMat = new THREE.MeshBasicMaterial({ color: 0x7fe9ff })
  const half = Math.PI / 2
  if (prov === 'claude') {
    part(g, new THREE.ConeGeometry(0.3, 0.8, 6), hull, [0.6, 0, 0], [0, 0, -half])
    part(g, new THREE.CylinderGeometry(0.3, 0.34, 1.0, 6), hull, [-0.2, 0, 0], [0, 0, half])
    part(g, new THREE.SphereGeometry(0.16, 8, 6), dark, [0.38, 0.2, 0])
    for (const s of [1, -1]) part(g, G.box, hull, [-0.3, -0.08, s * 0.5], [0, s * 0.45, 0], [0.7, 0.05, 0.62])
    part(g, G.box, hull, [-0.55, 0.3, 0], [0, 0, 0.35], [0.45, 0.42, 0.05])
    for (const s of [1, -1]) part(g, new THREE.CylinderGeometry(0.11, 0.11, 0.08, 8), engine, [-0.74, 0, s * 0.13], [0, 0, half])
  } else if (prov === 'codex') {
    part(g, G.box, hull, [0, 0, 0], [0, 0, 0], [1.25, 0.45, 0.6])
    part(g, G.box, dark, [0.72, 0.08, 0], [0, 0, 0], [0.3, 0.26, 0.42])
    for (const x of [-0.25, 0.2]) part(g, G.box, dark, [x, 0.24, 0], [0, 0, 0], [0.12, 0.06, 0.64])
    for (const s of [1, -1]) {
      part(g, new THREE.CylinderGeometry(0.14, 0.14, 0.95, 8), dark, [-0.1, -0.02, s * 0.44], [0, 0, half])
      part(g, new THREE.CylinderGeometry(0.12, 0.12, 0.05, 8), engine, [-0.6, -0.02, s * 0.44], [0, 0, half])
    }
  } else if (prov === 'gemini') {
    part(g, new THREE.SphereGeometry(0.66, 16, 8), hull, [0, 0, 0], [0, 0, 0], [1, 0.26, 1])
    part(g, new THREE.SphereGeometry(0.26, 12, 6, 0, Math.PI * 2, 0, half), dark, [0, 0.1, 0])
    part(g, new THREE.TorusGeometry(0.63, 0.03, 6, 24), engine, [0, 0, 0], [half, 0, 0])
  } else if (prov === 'goose') {
    // A slim body with a long nose, and wings swept back like a goose in flight.
    part(g, new THREE.ConeGeometry(0.16, 0.9, 6), hull, [0.55, 0.02, 0], [0, 0, -half])
    part(g, new THREE.CylinderGeometry(0.16, 0.2, 0.7, 6), hull, [-0.15, 0, 0], [0, 0, half])
    for (const s of [1, -1]) part(g, G.box, hull, [-0.2, 0.02, s * 0.55], [0, s * 0.62, 0], [0.32, 0.04, 1.05])
    part(g, new THREE.SphereGeometry(0.12, 8, 6), dark, [0.3, 0.14, 0])
    part(g, new THREE.CylinderGeometry(0.12, 0.12, 0.06, 8), engine, [-0.52, 0, 0], [0, 0, half])
  } else {
    part(g, new THREE.OctahedronGeometry(0.42, 0), hull, [0, 0, 0], [0, 0, 0], [1.1, 1, 1])
    part(g, new THREE.TorusGeometry(0.62, 0.06, 6, 20), dark, [0, 0, 0], [half, 0, 0])
    part(g, new THREE.SphereGeometry(0.1, 8, 6), engine, [0.42, 0, 0])
    for (let i = 0; i < 4; i++) {
      const a = (i / 4) * Math.PI * 2 + Math.PI / 4
      part(g, new THREE.CylinderGeometry(0.09, 0.11, 0.14, 6), dark, [Math.cos(a) * 0.62, -0.08, Math.sin(a) * 0.62])
      part(g, new THREE.CylinderGeometry(0.07, 0.07, 0.03, 6), engine, [Math.cos(a) * 0.62, -0.16, Math.sin(a) * 0.62])
    }
  }
  // The undercarriage, where an approval beam starts.
  part(g, G.box, dark, [0, -0.42, 0], [0, 0, 0], [0.36, 0.12, 0.26])
  const bags: THREE.Mesh[] = []
  for (const [x, z] of [
    [-0.26, 0.17],
    [0.26, 0.17],
    [-0.26, -0.17],
    [0.26, -0.17],
  ]) {
    bags.push(part(g, G.box, cellMat, [x, -0.52, z], [0, 0, 0], [0.13, 0.09, 0.13]))
  }
  return { group: g, mats: [hull, dark, engine, cellMat], bags }
}
