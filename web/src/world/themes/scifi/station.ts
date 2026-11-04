import * as THREE from 'three'
import { reduceMotion } from '../../../dom'
import { G } from '../../materials'
import { selectionRing } from '../../shapes'
import type { Citadel } from '../../theme'
import { commArray } from './comms'
import { S } from './materials'

/** A lit band around a drum: a short open cylinder just outside it. */
function band(g: THREE.Object3D, radius: number, y: number, height: number, segments: number): void {
  const m = new THREE.Mesh(new THREE.CylinderGeometry(radius, radius, height, segments, 1, true), S.glow)
  m.position.y = y
  g.add(m)
}

/** A drum of the station's hull. */
function drum(g: THREE.Object3D, top: number, bottom: number, height: number, y: number, mat: THREE.Material, segments = 16): void {
  const m = new THREE.Mesh(new THREE.CylinderGeometry(top, bottom, height, segments), mat)
  m.position.y = y
  g.add(m)
}

/**
 * The space station at the center of the world, in place of the castle: a
 * hub with a spinning habitat ring, solar arrays, and an antenna mast with
 * the beacon at its tip. A keel runs down to the world's NATS server, a
 * comms array every tether ends at.
 */
export function buildStation(): Citadel {
  const g = new THREE.Group()
  g.userData.host = 'cit'
  g.position.set(0, 8, 0)

  // the hub, with a band of lit windows, and the hull narrowing under it
  drum(g, 2.6, 2.8, 4.2, 2.1, S.hull)
  band(g, 2.84, 2.7, 0.32, 16)
  band(g, 2.76, 1.2, 0.12, 16)
  drum(g, 2.8, 1.3, 3, -1.5, S.hullDark)

  // the keel down to the reactor, ringed like a spine
  drum(g, 1.1, 0.4, 10, -8, S.trim, 10)
  for (const [y, r] of [
    [-4.5, 1.15],
    [-7.5, 0.95],
    [-10.5, 0.72],
  ]) {
    const rib = new THREE.Mesh(new THREE.TorusGeometry(r, 0.14, 6, 16), S.hullDark)
    rib.rotation.x = Math.PI / 2
    rib.position.y = y
    g.add(rib)
  }

  // the habitat ring, on spokes, which turns slowly
  const hab = new THREE.Group()
  hab.position.y = 2.1
  const RING = 7.2
  const torus = new THREE.Mesh(new THREE.TorusGeometry(RING, 0.75, 10, 48), S.hull)
  torus.rotation.x = Math.PI / 2
  hab.add(torus)
  for (let i = 0; i < 28; i++) {
    const a = (i / 28) * Math.PI * 2
    const w = new THREE.Mesh(G.box, S.window)
    w.scale.set(0.5, 0.22, 0.06)
    w.position.set(Math.cos(a) * (RING + 0.74), 0, Math.sin(a) * (RING + 0.74))
    w.rotation.y = -a + Math.PI / 2
    hab.add(w)
  }
  const spokeLen = RING - 2.7
  for (let i = 0; i < 4; i++) {
    const a = (i / 4) * Math.PI * 2 + Math.PI / 4
    const spoke = new THREE.Mesh(new THREE.CylinderGeometry(0.2, 0.2, spokeLen, 6), S.trim)
    spoke.rotation.z = Math.PI / 2
    const holder = new THREE.Group()
    holder.rotation.y = -a
    spoke.position.x = 2.7 + spokeLen / 2
    holder.add(spoke)
    hab.add(holder)
  }
  g.add(hab)

  // the upper tower, stepping in
  drum(g, 1.6, 2.2, 2.2, 5.3, S.hull, 12)
  band(g, 1.95, 5.6, 0.18, 12)
  drum(g, 1.0, 1.4, 2.6, 7.7, S.trim, 10)

  // solar arrays on a boom across the tower
  const boom = new THREE.Mesh(new THREE.CylinderGeometry(0.12, 0.12, 15.4, 6), S.trim)
  boom.rotation.z = Math.PI / 2
  boom.position.y = 7.7
  g.add(boom)
  for (const x of [-6.4, -3.6, 3.6, 6.4]) {
    const panel = new THREE.Mesh(G.box, S.panel)
    panel.scale.set(2.5, 0.06, 1.7)
    panel.position.set(x, 7.7, 0)
    g.add(panel)
  }

  // sensor head and antenna mast, with the beacon at the tip
  drum(g, 0.55, 0.9, 1.4, 9.7, S.hull, 10)
  const dish = new THREE.Mesh(new THREE.ConeGeometry(0.75, 0.35, 12, 1, true), S.trim)
  dish.rotation.x = Math.PI
  dish.position.set(0.9, 10.3, 0)
  dish.rotation.z = -0.6
  g.add(dish)
  drum(g, 0.07, 0.1, 2.4, 11.6, S.trim, 6)
  const top = 12.8
  const beacon = new THREE.Mesh(new THREE.SphereGeometry(0.5, 14, 10), new THREE.MeshBasicMaterial({ color: 0xffd54a }))
  beacon.position.y = top + 0.45
  g.add(beacon)
  const halo = new THREE.Mesh(
    new THREE.SphereGeometry(0.6, 14, 10),
    new THREE.MeshBasicMaterial({ color: 0xffd54a, transparent: true, opacity: 0.4, depthWrite: false }),
  )
  halo.position.copy(beacon.position)
  g.add(halo)
  const bl = new THREE.PointLight(0xffd54a, 1.1, 30, 2)
  bl.position.copy(beacon.position)
  g.add(bl)

  // navigation lights at the array tips and around the ring, red to port and green to starboard
  const red = new THREE.MeshBasicMaterial({ color: 0xff5a4d })
  const green = new THREE.MeshBasicMaterial({ color: 0x4dff8a })
  const navGeo = new THREE.SphereGeometry(0.16, 8, 6)
  for (const [x, mat] of [
    [-7.75, red],
    [7.75, green],
  ] as const) {
    const n = new THREE.Mesh(navGeo, mat)
    n.position.set(x, 7.7, 0)
    g.add(n)
  }
  for (let i = 0; i < 4; i++) {
    const a = (i / 4) * Math.PI * 2
    const n = new THREE.Mesh(navGeo, i % 2 ? green : red)
    n.position.set(Math.cos(a) * RING, 0.8, Math.sin(a) * RING)
    hab.add(n)
  }

  // The world's NATS server: a comms array under the keel, where every tether ends.
  const core = commArray(g, -1.6 - 12 - 1.8, 1.7, 2.8)
  const ring = selectionRing(10, 0.1)
  ring.position.y = 0.1
  g.add(ring)
  const lit = new THREE.Color()
  return {
    group: g,
    core,
    beacon,
    halo,
    light: bl,
    ring,
    labelY: top + 3,
    animate(time, dt) {
      if (!reduceMotion) hab.rotation.y += dt * 0.12
      // The navigation lights flash together, briefly, every two seconds.
      const on = reduceMotion || time % 2 < 0.22
      red.color.copy(lit.set(0xff5a4d).multiplyScalar(on ? 1 : 0.25))
      green.color.copy(lit.set(0x4dff8a).multiplyScalar(on ? 1 : 0.25))
    },
  }
}
