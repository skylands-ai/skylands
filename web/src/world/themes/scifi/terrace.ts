import * as THREE from 'three'
import { reduceMotion } from '../../../dom'
import { G } from '../../materials'
import { asBuilding, boundaryPoints, PLAZA_TOP, ropeColor, terraceLots, type Lot, type TerraceSpec } from '../../terrace'
import type { TerraceMeshes } from '../../theme'
import { BUILDINGS, S, SG, SG_EDGES } from './materials'

/** The lights around a terrace blink in a wave this many lights long. */
const WAVE = 8

/**
 * Adds a child to a building at a size in world units. The building is a
 * unit block stretched to its lot, so its children are scaled back.
 */
function fitted(b: THREE.Mesh, lot: Lot, geo: THREE.BufferGeometry, mat: THREE.Material, w: number, h: number, x: number, y: number): THREE.Mesh {
  const m = new THREE.Mesh(geo, mat)
  m.scale.set(w / 0.7, h / lot.height, w / 0.7)
  m.position.set(x / 0.7, y / lot.height, 0)
  b.add(m)
  return m
}

/** A hexagonal tower with a lit band under its roof, and another halfway up a tall one. */
function tower(t: TerraceSpec, lot: Lot): THREE.Mesh {
  const b = new THREE.Mesh(SG.hex, BUILDINGS.rest)
  asBuilding(b, lot, t, BUILDINGS)
  const edges = new THREE.LineSegments(SG_EDGES.hex, S.bldEdge)
  edges.scale.setScalar(1.006)
  // Outlines are not pickable: a line counts as hit anywhere near the ray.
  edges.raycast = () => {}
  b.add(edges)
  const half = lot.height / 2
  fitted(b, lot, SG.hexBand, S.bldBand, 0.73, 0.06, 0, half - 0.14)
  if (lot.height > 1.5) fitted(b, lot, SG.hexBand, S.bldBand, 0.73, 0.05, 0, 0)
  return b
}

/**
 * A terrace in the sci-fi theme: a metal deck with glowing edges, a tower
 * per file, and landing-strip lights on poles around it in the repo's
 * color. The lights blink slowly in a wave around the edge, faster and
 * brighter while the terrace is selected.
 */
export function buildTerrace(t: TerraceSpec, seed: number): TerraceMeshes {
  const g = new THREE.Group()
  const deck = new THREE.Mesh(G.box, S.plaza)
  deck.scale.set(t.sx, PLAZA_TOP, t.sz)
  deck.position.set(t.x, PLAZA_TOP / 2, t.z)
  deck.userData.terraceId = t.id
  g.add(deck)
  for (const [w, d, x, z] of [
    [t.sx, 0.05, t.x, t.z - t.sz / 2],
    [t.sx, 0.05, t.x, t.z + t.sz / 2],
    [0.05, t.sz, t.x - t.sx / 2, t.z],
    [0.05, t.sz, t.x + t.sx / 2, t.z],
  ]) {
    const edge = new THREE.Mesh(G.box, S.plazaEdge)
    edge.scale.set(w, 0.04, d)
    edge.position.set(x, PLAZA_TOP + 0.01, z)
    edge.userData.terraceId = t.id
    g.add(edge)
  }

  const blds = terraceLots(t, seed).map((lot) => {
    const b = tower(t, lot)
    g.add(b)
    return b
  })

  // Each light in a wave has its own lamp and glow, shared with the lights
  // a wave further along.
  const color = new THREE.Color(ropeColor(t.id))
  const lamps = Array.from({ length: WAVE }, () => new THREE.MeshBasicMaterial({ color }))
  const glows = Array.from({ length: WAVE }, () => new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.35, depthWrite: false }))
  boundaryPoints(t, 0.24, 0.8).forEach((p, i) => {
    const pole = new THREE.Mesh(SG.pole, S.pole)
    pole.position.set(p[0], 0.425, p[1])
    const lamp = new THREE.Mesh(SG.lamp, lamps[i % WAVE])
    lamp.position.set(p[0], 0.9, p[1])
    const glow = new THREE.Mesh(SG.halo, glows[i % WAVE])
    glow.position.copy(lamp.position)
    glow.raycast = () => {}
    for (const m of [pole, lamp, glow]) {
      m.userData.terraceId = t.id
      g.add(m)
    }
  })

  const lit = new THREE.Color()
  return {
    group: g,
    blds,
    animate(time, selected) {
      const speed = selected ? 0.9 : 0.3
      for (let k = 0; k < WAVE; k++) {
        let level: number
        if (reduceMotion) level = selected ? 1 : 0.75
        else {
          // A light flashes as the wave passes it, then fades back to a glow.
          const phase = (((time * speed - k / WAVE) % 1) + 1) % 1
          const flash = phase < 0.12 ? 1 : Math.max(0, 1 - (phase - 0.12) * 5)
          level = (selected ? 0.35 : 0.18) + (selected ? 0.65 : 0.82) * flash
        }
        lamps[k].color.copy(lit.copy(color).multiplyScalar(level))
        glows[k].opacity = 0.45 * level
      }
    },
  }
}
