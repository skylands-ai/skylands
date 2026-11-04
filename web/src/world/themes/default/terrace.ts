import * as THREE from 'three'
import { reduceMotion } from '../../../dom'
import { G, M, std } from '../../materials'
import { asBuilding, boundaryPoints, PLAZA_TOP, ropeColor, terraceLots, type TerraceSpec } from '../../terrace'
import { markShared, type BuildingMats, type TerraceMeshes } from '../../theme'

/** Pale stone buildings, amber while edited, blue while read, red in conflict, cyan while selected. */
export const BUILDINGS: BuildingMats = { rest: M.bld, read: M.bldRead, edit: M.bldEdit, conflict: M.bldConflict, selected: M.bldSelected }

/** The rope's resting glow. */
const ROPE_GLOW = 0.25

const POST_MAT = std(0xb8954a, { metalness: 0.45, roughness: 0.45 })
const POST_GEO = new THREE.CylinderGeometry(0.045, 0.06, 0.6, 6)
const KNOB_GEO = new THREE.SphereGeometry(0.08, 8, 6)
const FOOT_GEO = new THREE.CylinderGeometry(0.12, 0.14, 0.05, 8)
markShared([POST_MAT, POST_GEO, KNOB_GEO, FOOT_GEO])

/** Stanchions with a colored rope around a terrace: the repository boundary. Returns the rope's material. */
function addStanchions(g: THREE.Group, t: TerraceSpec): THREE.MeshStandardMaterial {
  const col = ropeColor(t.id)
  const ropeMat = new THREE.MeshStandardMaterial({ color: col, emissive: col, emissiveIntensity: ROPE_GLOW, roughness: 0.8 })
  const base = PLAZA_TOP
  const top = base + 0.6
  const anchor = top - 0.07
  const pts = boundaryPoints(t, 0.14, 1.1)
  pts.forEach((p, i) => {
    const foot = new THREE.Mesh(FOOT_GEO, POST_MAT)
    foot.position.set(p[0], base + 0.025, p[1])
    const post = new THREE.Mesh(POST_GEO, POST_MAT)
    post.position.set(p[0], base + 0.3, p[1])
    const knob = new THREE.Mesh(KNOB_GEO, POST_MAT)
    knob.position.set(p[0], top + 0.04, p[1])
    const q = pts[(i + 1) % pts.length]
    const curve = new THREE.QuadraticBezierCurve3(
      new THREE.Vector3(p[0], anchor, p[1]),
      new THREE.Vector3((p[0] + q[0]) / 2, anchor - 0.32, (p[1] + q[1]) / 2),
      new THREE.Vector3(q[0], anchor, q[1]),
    )
    const rope = new THREE.Mesh(new THREE.TubeGeometry(curve, 10, 0.035, 5, false), ropeMat)
    for (const m of [foot, post, knob, rope]) {
      m.userData.terraceId = t.id
      g.add(m)
    }
  })
  return ropeMat
}

/**
 * A terrace: a plaza with a rope around it and a building per file, its
 * height scaled by file size. The rope pulses while the terrace is selected.
 */
export function buildTerrace(t: TerraceSpec, seed: number): TerraceMeshes {
  const g = new THREE.Group()
  const plaza = new THREE.Mesh(G.box, M.plaza)
  plaza.scale.set(t.sx, PLAZA_TOP, t.sz)
  plaza.position.set(t.x, PLAZA_TOP / 2, t.z)
  plaza.userData.terraceId = t.id
  g.add(plaza)
  const rope = addStanchions(g, t)

  const blds = terraceLots(t, seed).map((lot) => {
    const b = new THREE.Mesh(G.box, M.bld)
    asBuilding(b, lot, t, BUILDINGS)
    // Outline every edge, so buildings keep their individuality at low angles.
    // The outline sits a hair outside the faces, so no renderer can hide it.
    const edges = new THREE.LineSegments(G.boxEdges, M.bldEdge)
    edges.scale.setScalar(1.006)
    // Outlines are not pickable. A line counts as hit anywhere near the ray,
    // so a neighbor's edge would steal clicks meant for this roof.
    edges.raycast = () => {}
    b.add(edges)
    g.add(b)
    return b
  })
  return {
    group: g,
    blds,
    animate(time, selected) {
      const glow = selected ? (reduceMotion ? 0.7 : 0.45 + 0.4 * Math.sin(time * 2.6)) : ROPE_GLOW
      if (rope.emissiveIntensity !== glow) rope.emissiveIntensity = glow
    },
  }
}
