import * as THREE from 'three'
import { reduceMotion } from '../../../dom'
import { G } from '../../materials'
import { hash3, ringNoise, rng } from '../../noise'
import type { Placement } from '../../placement'
import { selectionRing } from '../../shapes'
import type { IslandMeshes } from '../../theme'
import { commArray } from './comms'
import { S, SG } from './materials'

type Noise = (t: number) => number

/** How thick the landing surface on top of an asteroid is. */
const PLATE = 0.3

/** How deep an asteroid's rock reaches, as a share of the island's depth. */
const BODY = 0.8

/**
 * The rock of an asteroid: a lumpy ball, its upper half sliced flat where
 * the landing surface sits, darker and pitted with craters further down.
 * Every lump comes from the island's seed, so it keeps its shape.
 */
function asteroidBody(R: number, depth: number, noise: Noise, seed: number): THREE.BufferGeometry {
  const geo = new THREE.IcosahedronGeometry(1, 2)
  const pos = geo.attributes.position
  const colors: number[] = []
  const light = new THREE.Color(0x857c74)
  const dark = new THREE.Color(0x48423e)
  const tmp = new THREE.Color()
  for (let i = 0; i < pos.count; i++) {
    const x = pos.getX(i)
    const y = pos.getY(i)
    const z = pos.getZ(i)
    const n = noise(Math.atan2(z, x))
    const lump = 1 + (hash3(x * 3, y * 3 + seed, z * 3) - 0.5) * 0.32 + 0.1 * Math.sin(x * 4 + seed) * Math.cos(z * 3 + seed)
    const below = Math.min(0, y)
    pos.setXYZ(i, x * R * n * lump, below * depth * lump, z * R * n * lump)
    const t = -below
    tmp.copy(light).lerp(dark, t)
    const crater = hash3(x * 6 + seed, y * 6, z * 6) > 0.86 ? 0.68 : 1
    const v = (0.9 + hash3(z, x, y + seed) * 0.2) * crater
    colors.push(tmp.r * v, tmp.g * v, tmp.b * v)
  }
  geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3))
  geo.computeVertexNormals()
  return geo
}

/** The flat landing surface on top, following the island's outline. */
function plateGeo(R: number, noise: Noise): THREE.BufferGeometry {
  const geo = new THREE.CylinderGeometry(R, R * 0.99, PLATE, 28, 1)
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

/**
 * One host's island in the sci-fi theme: an asteroid with a flattened
 * landing surface on top, boulders, and small outposts whose masts blink.
 * Nothing is painted on the surface: a ring there would read as a
 * selection, which the island's turning ring and a terrace's lights show.
 * Everything is seeded from the placement, so it looks the same on every
 * visit.
 */
export function buildAsteroid(host: string, h: Placement): IslandMeshes {
  const g = new THREE.Group()
  g.userData.host = host
  const noise = ringNoise(h.seed)
  const r = rng(h.seed * 7 + 3)
  const plate = new THREE.Mesh(plateGeo(h.R, noise), [S.rockSide, S.deck, S.rockSide])
  plate.position.y = -PLATE / 2
  g.add(plate)
  const bodyGeo = asteroidBody(h.R * 0.98, h.depth * BODY, noise, h.seed)
  const body = new THREE.Mesh(bodyGeo, S.rock)
  body.position.y = -PLATE
  g.add(body)
  // The comms array hangs under the rock's lowest point, its mast reaching up into it.
  bodyGeo.computeBoundingBox()
  const bottom = -PLATE + (bodyGeo.boundingBox as THREE.Box3).min.y
  const core = commArray(g, bottom - 1.6, 1, 2)

  // boulders, which a terrace clears like a hill
  for (let k = 0; k < 3; k++) {
    const a = r() * 6.283
    const d = (0.4 + r() * 0.42) * h.R
    const rock = new THREE.Mesh(SG.boulder, S.boulder)
    rock.scale.set(0.5 + r() * 0.5, 0.3 + r() * 0.25, 0.45 + r() * 0.45)
    rock.position.set(Math.cos(a) * d, 0, Math.sin(a) * d)
    rock.rotation.y = r() * 6.283
    rock.userData.scenery = 1.2
    g.add(rock)
  }

  // outposts: domes, dishes, and masts with a blinking light
  const lamp = new THREE.MeshBasicMaterial({ color: 0xff5a4d })
  const outposts = Math.round(h.R * 1.1)
  for (let i = 0; i < outposts; i++) {
    const a = r() * 6.283
    const d = (0.3 + Math.sqrt(r()) * 0.55) * h.R * noise(a)
    const o = new THREE.Group()
    o.position.set(Math.cos(a) * d, 0, Math.sin(a) * d)
    o.rotation.y = r() * 6.283
    const kind = r()
    if (kind < 0.4) {
      const dome = new THREE.Mesh(new THREE.SphereGeometry(0.42, 10, 6, 0, Math.PI * 2, 0, Math.PI / 2), S.hull)
      o.add(dome)
      const ring = new THREE.Mesh(new THREE.CylinderGeometry(0.43, 0.43, 0.06, 10, 1, true), S.glow)
      ring.position.y = 0.06
      o.add(ring)
    } else if (kind < 0.7) {
      const stand = new THREE.Mesh(new THREE.CylinderGeometry(0.05, 0.08, 0.4, 6), S.trim)
      stand.position.y = 0.2
      const dish = new THREE.Mesh(new THREE.ConeGeometry(0.32, 0.16, 10, 1, true), S.hull)
      dish.rotation.set(Math.PI - 0.6, 0, 0)
      dish.position.y = 0.45
      o.add(stand, dish)
    } else {
      const mast = new THREE.Mesh(new THREE.CylinderGeometry(0.035, 0.05, 1.1, 5), S.trim)
      mast.position.y = 0.55
      const tip = new THREE.Mesh(SG.lamp, lamp)
      tip.position.y = 1.12
      const base = new THREE.Mesh(G.box, S.hullDark)
      base.scale.set(0.3, 0.12, 0.3)
      base.position.y = 0.06
      o.add(mast, tip, base)
    }
    o.scale.setScalar(0.85 + r() * 0.4)
    o.userData.scenery = 0.7
    g.add(o)
  }

  const ring = selectionRing(h.R * 1.18, 0.09)
  ring.position.y = 0.15
  g.add(ring)
  const color = new THREE.Color()
  return {
    group: g,
    core,
    ring,
    animate(time) {
      // The masts blink slowly, each island in its own rhythm.
      const on = reduceMotion || (time * 0.6 + h.phase) % 1 < 0.18
      lamp.color.copy(color.set(0xff5a4d).multiplyScalar(on ? 1 : 0.2))
    },
  }
}
