import * as THREE from 'three'
import { G, M, std } from './materials'
import { hash3 } from './noise'

export const SLAB = 1.4

type Noise = (t: number) => number
type Size = [number, number, number]

/** The jagged rock hanging under an island or the citadel, colored by depth. */
export function underside(R: number, depth: number, noise: Noise, seed: number, cols: [number, number, number]): THREE.BufferGeometry {
  const geo = new THREE.ConeGeometry(R, depth, 22, 6)
  const pos = geo.attributes.position
  const colors: number[] = []
  const c0 = new THREE.Color(cols[0])
  const c1 = new THREE.Color(cols[1])
  const c2 = new THREE.Color(cols[2])
  const tmp = new THREE.Color()
  for (let i = 0; i < pos.count; i++) {
    const x = pos.getX(i)
    const y = pos.getY(i)
    const z = pos.getZ(i)
    const t = (y + depth / 2) / depth
    const n = noise(Math.atan2(z, x))
    const j = hash3(x, y + seed, z)
    const mid = t > 0.02 && t < 0.98
    const radial = mid ? 1 + (j - 0.5) * 0.45 : 1
    const yj = mid ? (hash3(z, x, y + seed * 2) - 0.5) * depth * 0.08 : 0
    pos.setXYZ(i, x * n * radial, y + yj, z * n * radial)
    if (t < 0.5) tmp.copy(c0).lerp(c1, t / 0.5)
    else tmp.copy(c1).lerp(c2, (t - 0.5) / 0.5)
    const v = 0.88 + j * 0.24
    colors.push(tmp.r * v, tmp.g * v, tmp.b * v)
  }
  geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3))
  geo.rotateX(Math.PI)
  geo.computeVertexNormals()
  return geo
}

/** The flat top of an island, wobbled by the same noise as its underside. */
export function slabGeo(R: number, noise: Noise): THREE.BufferGeometry {
  const geo = new THREE.CylinderGeometry(R, R * 0.97, SLAB, 22, 1)
  const p = geo.attributes.position
  for (let i = 0; i < p.count; i++) {
    const x = p.getX(i)
    const z = p.getZ(i)
    const n = noise(Math.atan2(z, x))
    p.setXYZ(i, x * n, p.getY(i), z * n)
  }
  geo.computeVertexNormals()
  return geo
}

export interface Crystal {
  mesh: THREE.Mesh
  mat: THREE.MeshStandardMaterial
  light: THREE.PointLight
  /** Where the crystal sits in its group, which is where tethers attach. */
  local: THREE.Vector3
}

/** The glowing crystal under an island or the citadel. Lit while its link is up. */
export function crystalAt(group: THREE.Object3D, y: number, size: number): Crystal {
  const mat = std(0x7fe0f0, { emissive: 0x3fc9e0, emissiveIntensity: 1.1 })
  const m = new THREE.Mesh(G.crystal, mat)
  m.scale.setScalar(size)
  m.position.set(0, y, 0)
  group.add(m)
  const light = new THREE.PointLight(0x7fe0f0, 1.3, 16, 2)
  light.position.set(0, y, 0)
  group.add(light)
  return { mesh: m, mat, light, local: new THREE.Vector3(0, y, 0) }
}

export function crenelRing(g: THREE.Object3D, r: number, y: number, count: number, size: Size): void {
  for (let i = 0; i < count; i++) {
    const a = (i / count) * Math.PI * 2
    const m = new THREE.Mesh(G.box, M.stoneTop)
    m.scale.set(size[0], size[1], size[2])
    m.position.set(Math.cos(a) * r, y + size[1] / 2, Math.sin(a) * r)
    m.rotation.y = -a + Math.PI / 2
    g.add(m)
  }
}

export function crenelSquare(g: THREE.Object3D, half: number, y: number, perSide: number, size: Size): void {
  for (let side = 0; side < 4; side++) {
    for (let i = 0; i < perSide; i++) {
      const t = -half + (i + 0.5) * ((2 * half) / perSide)
      const m = new THREE.Mesh(G.box, M.stoneTop)
      m.scale.set(size[0], size[1], size[2])
      const x = side === 0 ? t : side === 1 ? half : side === 2 ? -t : -half
      const z = side === 0 ? -half : side === 1 ? t : side === 2 ? half : -t
      m.position.set(x, y + size[1] / 2, z)
      m.rotation.y = side % 2 ? Math.PI / 2 : 0
      g.add(m)
    }
  }
}

/** A lit window slit in a wall. */
export function slit(g: THREE.Object3D, x: number, y: number, z: number, rotY: number): void {
  const w = new THREE.Mesh(G.box, M.window)
  w.scale.set(0.22, 0.6, 0.08)
  w.position.set(x, y, z)
  w.rotation.y = rotY
  g.add(w)
}
