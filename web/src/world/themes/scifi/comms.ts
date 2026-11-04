import * as THREE from 'three'
import { std } from '../../materials'
import type { Crystal } from '../../shapes'
import { S } from './materials'

/** The link glow while the link is up, as the world sets it. */
const LIT = 0x7fe0f0

/**
 * A dish: a shallow reflector looking down its local -y, with a feed horn
 * at its focus held on three struts. The feed glows with the link.
 */
function dish(radius: number, glow: THREE.Material): THREE.Group {
  const g = new THREE.Group()
  const open = 0.72
  const cap = new THREE.Mesh(new THREE.SphereGeometry(radius, 18, 5, 0, Math.PI * 2, 0, open), S.dish)
  cap.position.y = -radius
  g.add(cap)
  const rimY = -radius * (1 - Math.cos(open))
  const rimR = radius * Math.sin(open)
  const focus = new THREE.Vector3(0, -radius * 0.55, 0)
  for (let i = 0; i < 3; i++) {
    const a = (i / 3) * Math.PI * 2
    const from = new THREE.Vector3(Math.cos(a) * rimR * 0.92, rimY, Math.sin(a) * rimR * 0.92)
    const len = from.distanceTo(focus)
    const strut = new THREE.Mesh(new THREE.CylinderGeometry(radius * 0.018, radius * 0.018, len, 4), S.trim)
    strut.position.copy(from).add(focus).multiplyScalar(0.5)
    strut.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), focus.clone().sub(from).normalize())
    g.add(strut)
  }
  const feed = new THREE.Mesh(new THREE.CylinderGeometry(radius * 0.07, radius * 0.11, radius * 0.2, 8), S.hullDark)
  feed.position.copy(focus)
  g.add(feed)
  const emitter = new THREE.Mesh(new THREE.SphereGeometry(radius * 0.09, 10, 8), glow)
  emitter.position.copy(focus).add(new THREE.Vector3(0, -radius * 0.1, 0))
  g.add(emitter)
  return g
}

/**
 * A communications array hanging under an asteroid or the space station,
 * in place of the default theme's crystal: a mast reaching up into what is
 * above, and under it a head that pans slowly while the link is up, with a
 * large dish looking out and down, a small one opposite, and a whip
 * antenna. The feeds, a ring on the head, and the whip's tip glow with the
 * link. Tethers attach at the head.
 */
export function commArray(group: THREE.Object3D, y: number, size: number, reach: number): Crystal {
  const glow = std(LIT, { emissive: 0x3fc9e0, emissiveIntensity: 1.1 })

  // The mast stays put; the head under it turns.
  const mast = new THREE.Mesh(new THREE.CylinderGeometry(0.09 * size, 0.12 * size, reach, 6), S.trim)
  mast.position.set(0, y + reach / 2, 0)
  group.add(mast)
  const collar = new THREE.Mesh(new THREE.TorusGeometry(0.2 * size, 0.05 * size, 6, 14), S.hullDark)
  collar.rotation.x = Math.PI / 2
  collar.position.set(0, y + 0.3 * size, 0)
  group.add(collar)

  const head = new THREE.Group()
  head.position.set(0, y, 0)
  const hub = new THREE.Mesh(new THREE.CylinderGeometry(0.24 * size, 0.3 * size, 0.5 * size, 10), S.hullDark)
  head.add(hub)
  const ring = new THREE.Mesh(new THREE.TorusGeometry(0.31 * size, 0.04 * size, 6, 16), glow)
  ring.rotation.x = Math.PI / 2
  ring.position.y = -0.12 * size
  head.add(ring)

  // The main dish on a yoke, looking out and down.
  const yoke = new THREE.Mesh(new THREE.BoxGeometry(0.62 * size, 0.09 * size, 0.14 * size), S.trim)
  yoke.position.set(0.4 * size, -0.05 * size, 0)
  head.add(yoke)
  const big = dish(1.05 * size, glow)
  big.position.set(0.72 * size, -0.05 * size, 0)
  big.rotation.z = 0.95
  head.add(big)

  // A small dish the other way, and a whip antenna below.
  const small = dish(0.45 * size, glow)
  small.position.set(-0.42 * size, -0.1 * size, 0)
  small.rotation.z = -1.1
  head.add(small)
  const whip = new THREE.Mesh(new THREE.CylinderGeometry(0.015 * size, 0.03 * size, 1.1 * size, 4), S.trim)
  whip.position.y = -0.8 * size
  head.add(whip)
  const tip = new THREE.Mesh(new THREE.SphereGeometry(0.06 * size, 8, 6), glow)
  tip.position.y = -1.37 * size
  head.add(tip)
  group.add(head)

  const light = new THREE.PointLight(LIT, 1.3, 16, 2)
  light.position.set(0, y, 0)
  group.add(light)
  return { mesh: head, mat: glow, light, local: new THREE.Vector3(0, y, 0) }
}
