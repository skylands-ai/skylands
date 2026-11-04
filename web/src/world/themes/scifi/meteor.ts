import * as THREE from 'three'
import type { Visitor } from '../../theme'
import { viewAt } from '../../visitors'

/** How far from the camera it streaks: beyond the world, in front of the stars. */
const DISTANCE = 320

/**
 * A shooting star that streaks down across the starfield in front of the
 * camera: a bright head with a tail that grows behind it and fades, then
 * the whole streak fades out.
 */
export function shootingStar(): Visitor {
  // A tapered quad, head to tail, drawn edge-on to nothing: it always faces the camera.
  const geo = new THREE.BufferGeometry()
  const pos = new THREE.BufferAttribute(new Float32Array(4 * 3), 3)
  geo.setAttribute('position', pos)
  // Head white and opaque, tail bluish and clear.
  geo.setAttribute('color', new THREE.Float32BufferAttribute([1, 1, 1, 1, 1, 1, 1, 1, 0.6, 0.75, 1, 0, 0.6, 0.75, 1, 0], 4))
  geo.setIndex([0, 2, 1, 1, 2, 3])
  const tailMat = new THREE.MeshBasicMaterial({ vertexColors: true, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, fog: false, side: THREE.DoubleSide })
  const tail = new THREE.Mesh(geo, tailMat)
  tail.frustumCulled = false

  const headGeo = new THREE.BufferGeometry()
  const headPos = new THREE.BufferAttribute(new Float32Array(3), 3)
  headGeo.setAttribute('position', headPos)
  const headMat = new THREE.PointsMaterial({ color: 0xffffff, size: 3.5, sizeAttenuation: false, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, fog: false })
  const head = new THREE.Points(headGeo, headMat)
  head.frustumCulled = false

  const group = new THREE.Group()
  group.add(tail, head)

  const from = new THREE.Vector3()
  const to = new THREE.Vector3()
  const dir = new THREE.Vector3()
  const at = new THREE.Vector3()
  const back = new THREE.Vector3()
  const side = new THREE.Vector3()
  const view = new THREE.Vector3()
  let width = 1
  let length = 1
  return {
    object: group,
    duration: 1.4,
    begin(camera) {
      const v = viewAt(camera, DISTANCE)
      // From somewhere in the upper part of the view, down and to one side.
      const sideways = Math.random() < 0.5 ? -1 : 1
      const angle = 0.3 + Math.random() * 0.45
      from.copy(v.center).addScaledVector(v.right, (Math.random() * 1.2 - 0.6) * v.halfW).addScaledVector(v.up, (0.3 + Math.random() * 0.5) * v.halfH)
      dir.copy(v.right).multiplyScalar(sideways * Math.cos(angle)).addScaledVector(v.up, -Math.sin(angle)).normalize()
      const travel = v.halfW * (0.5 + Math.random() * 0.35)
      to.copy(from).addScaledVector(dir, travel)
      width = v.halfH * 0.006
      length = travel * 0.45
    },
    step(t, _time, camera) {
      // The head slows as it burns out; the tail grows, then everything fades.
      const p = 1 - (1 - Math.min(1, t / 0.85)) ** 2
      at.lerpVectors(from, to, p)
      back.copy(at).addScaledVector(dir, -length * Math.min(1, t * 3))
      side.crossVectors(dir, view.subVectors(camera.position, at)).normalize().multiplyScalar(width)
      pos.setXYZ(0, at.x + side.x, at.y + side.y, at.z + side.z)
      pos.setXYZ(1, at.x - side.x, at.y - side.y, at.z - side.z)
      pos.setXYZ(2, back.x + side.x * 0.15, back.y + side.y * 0.15, back.z + side.z * 0.15)
      pos.setXYZ(3, back.x - side.x * 0.15, back.y - side.y * 0.15, back.z - side.z * 0.15)
      pos.needsUpdate = true
      headPos.setXYZ(0, at.x, at.y, at.z)
      headPos.needsUpdate = true
      const fade = t < 0.7 ? 1 : (1 - t) / 0.3
      tailMat.opacity = headMat.opacity = fade
    },
  }
}
