import * as THREE from 'three'

/** Flat-shaded standard material, the look of everything solid in the world. */
export function std(color: number, extra?: THREE.MeshStandardMaterialParameters): THREE.MeshStandardMaterial {
  return new THREE.MeshStandardMaterial({ color, flatShading: true, roughness: 0.92, metalness: 0, ...extra })
}

/** Building faces sit a hair behind their edge lines, so the strokes never flicker. */
const offset: THREE.MeshStandardMaterialParameters = { polygonOffset: true, polygonOffsetFactor: 1, polygonOffsetUnits: 1 }

/** Shared materials, named for what they color. */
export const M = {
  grass: std(0x5e8f4e),
  soil: std(0x7a5a3e),
  rock: std(0xffffff, { vertexColors: true }),
  plaza: std(0x3a3531),
  bld: std(0xe2dccb, { emissive: 0x34302a, emissiveIntensity: 1, ...offset }),
  bldEdit: std(0xf4e7c2, { emissive: 0x8a6a20, emissiveIntensity: 0.6, ...offset }),
  bldRead: std(0xdce8f4, { emissive: 0x2f5f95, emissiveIntensity: 0.45, ...offset }),
  bldConflict: std(0xf3c6be, { emissive: 0xa8352a, emissiveIntensity: 0.7, ...offset }),
  /** The selected building: a cool glow that breathes, distinct from the read, edit, and conflict colors. */
  bldSelected: std(0xf4fbfd, { emissive: 0x3fc9e0, emissiveIntensity: 0.5, ...offset }),
  /** The single-stroke outline on every building edge. */
  bldEdge: new THREE.LineBasicMaterial({ color: 0x4a4238, transparent: true, opacity: 0.6 }),
  trunk: std(0x6b4a30),
  leafA: std(0x3f7a3a),
  leafB: std(0x4f8f45),
  leafC: std(0x2f6a33),
  hill: std(0x6a9a55),
  stoneTop: std(0xd3d8df),
  stoneWall: std(0x9aa6b4),
  stoneDark: std(0x7f8ca0),
  window: new THREE.MeshBasicMaterial({ color: 0xffe3a3 }),
  ring: new THREE.MeshBasicMaterial({ color: 0xffffff, transparent: true, opacity: 0.9 }),
  gondola: std(0x3a3f4a),
  sand: std(0xc8b48a),
  door: std(0x4a3a2a),
  stoneKeep: std(0xa9b3bf),
  beam: new THREE.MeshBasicMaterial({ color: 0xffd54a, transparent: true, opacity: 0.85 }),
  beamBad: new THREE.MeshBasicMaterial({ color: 0xff7a6e, transparent: true, opacity: 0.85 }),
  beamTop: new THREE.MeshBasicMaterial({ color: 0xffe27a }),
  beamTopBad: new THREE.MeshBasicMaterial({ color: 0xff9c92 }),
  leash: new THREE.LineBasicMaterial({ color: 0xf2995e, transparent: true, opacity: 0.8 }),
}

/** Shared geometries, reused by every mesh of that shape. */
export const G = {
  box: new THREE.BoxGeometry(1, 1, 1),
  trunk: new THREE.CylinderGeometry(0.08, 0.12, 0.6, 5),
  pine: new THREE.ConeGeometry(0.5, 1.3, 6),
  round: new THREE.IcosahedronGeometry(0.55, 0),
  hill: new THREE.IcosahedronGeometry(1, 0),
  crystal: new THREE.OctahedronGeometry(0.9, 0),
  /** The twelve edges of the unit box, for building outlines. */
  boxEdges: new THREE.EdgesGeometry(new THREE.BoxGeometry(1, 1, 1)),
  beam: new THREE.CylinderGeometry(0.09, 0.09, 1, 6),
  ball: new THREE.SphereGeometry(0.35, 10, 8),
  bag: new THREE.SphereGeometry(0.13, 6, 5),
}
G.trunk.translate(0, 0.3, 0)
G.pine.translate(0, 1.1, 0)
G.round.translate(0, 0.95, 0)
