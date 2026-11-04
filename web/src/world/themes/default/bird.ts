import * as THREE from 'three'
import type { Visitor } from '../../theme'
import { viewAt } from '../../visitors'

/** One flap-and-glide cycle, in seconds, and how much of it is flapping. */
const CYCLE = 2.4
const FLAPPING = 1.4

/** A wing on the +x side, its root along the body and its tip swept back. */
function wingGeometry(): THREE.BufferGeometry {
  const g = new THREE.BufferGeometry()
  // root front, tip front, tip back, root back
  g.setAttribute('position', new THREE.Float32BufferAttribute([0, 0, 0.14, 0.82, 0, -0.06, 0.7, 0, -0.17, 0, 0, -0.12], 3))
  g.setIndex([0, 1, 3, 1, 2, 3])
  g.computeVertexNormals()
  return g
}

/**
 * A bird that flies across the sky in front of the camera, flapping and
 * gliding by turns. It is a silhouette about three world units from
 * wingtip to wingtip, facing +z.
 */
export function bird(): Visitor {
  const mat = new THREE.MeshStandardMaterial({ color: 0x3b4048, roughness: 0.9, flatShading: true, side: THREE.DoubleSide })
  const group = new THREE.Group()
  const body = new THREE.Mesh(new THREE.ConeGeometry(0.08, 0.6, 5), mat)
  body.rotation.x = Math.PI / 2 // the cone's tip, its beak, points forward
  group.add(body)
  const tail = new THREE.Mesh(new THREE.BufferGeometry(), mat)
  tail.geometry.setAttribute('position', new THREE.Float32BufferAttribute([0, 0, -0.22, 0.12, 0, -0.42, -0.12, 0, -0.42], 3))
  tail.geometry.computeVertexNormals()
  group.add(tail)
  const wing = wingGeometry()
  const right = new THREE.Group()
  right.position.x = 0.04
  right.add(new THREE.Mesh(wing, mat))
  const left = new THREE.Group()
  left.position.x = -0.04
  const leftWing = new THREE.Mesh(wing, mat)
  leftWing.scale.x = -1
  left.add(leftWing)
  group.add(right, left)
  group.scale.setScalar(1.8)

  const from = new THREE.Vector3()
  const to = new THREE.Vector3()
  const up = new THREE.Vector3()
  const ahead = new THREE.Vector3()
  return {
    object: group,
    duration: 7,
    begin(camera) {
      // Across the upper part of the view, where the sky is, rising or sinking
      // a little, and nearing or leaving the camera, so it isn't seen only
      // side on.
      const d = 30 + Math.random() * 14
      const dz = (Math.random() - 0.5) * 18
      const v0 = viewAt(camera, d + dz)
      const v1 = viewAt(camera, d - dz)
      const side = Math.random() < 0.5 ? -1 : 1
      const y0 = 0.15 + Math.random() * 0.5
      const y1 = y0 + (Math.random() - 0.5) * 0.3
      from.copy(v0.center).addScaledVector(v0.right, -side * 1.2 * v0.halfW).addScaledVector(v0.up, y0 * v0.halfH)
      to.copy(v1.center).addScaledVector(v1.right, side * 1.2 * v1.halfW).addScaledVector(v1.up, y1 * v1.halfH)
      up.copy(v0.up)
    },
    step(t, time) {
      group.position.lerpVectors(from, to, t).addScaledVector(up, Math.sin(t * Math.PI * 3) * 0.35)
      group.lookAt(ahead.subVectors(to, from).add(group.position))
      // Flap, then glide with the wings held a little up, easing between the two.
      const phase = time % CYCLE
      const flap = 0.6 * Math.sin(time * 15)
      const glide = 0.14
      const k = Math.min(Math.min(phase / 0.25, 1), Math.max(0, Math.min(1, (FLAPPING - phase) / 0.25 + 1)))
      const a = glide + (flap - glide) * (phase < FLAPPING ? k : 0)
      right.rotation.z = a
      left.rotation.z = -a
    },
  }
}
