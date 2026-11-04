import * as THREE from 'three'
import { std } from '../../materials'
import { markShared, type BuildingMats } from '../../theme'

/** Building faces sit a hair behind their edge lines, so the strokes never flicker. */
const offset: THREE.MeshStandardMaterialParameters = { polygonOffset: true, polygonOffsetFactor: 1, polygonOffsetUnits: 1 }

/** The sci-fi theme's shared materials, named for what they color. */
export const S = {
  hull: std(0xc3cad4, { metalness: 0.35, roughness: 0.55 }),
  hullDark: std(0x56606e, { metalness: 0.4, roughness: 0.5 }),
  trim: std(0x8a95a5, { metalness: 0.5, roughness: 0.4 }),
  /** Solar panels: deep blue with a faint glow, so they read against space. */
  panel: std(0x1f3358, { emissive: 0x0f2a55, emissiveIntensity: 0.7, metalness: 0.5, roughness: 0.35 }),
  /** A dish's reflector, seen from either side. */
  dish: std(0xd6dce4, { metalness: 0.3, roughness: 0.45, side: THREE.DoubleSide }),
  /** Lit trim: window bands, deck edges, engine glows. */
  glow: new THREE.MeshBasicMaterial({ color: 0x6fe6ff }),
  window: new THREE.MeshBasicMaterial({ color: 0xcff4ff }),
  /** An asteroid's landing surface: smoothed grey regolith. */
  deck: std(0x5a5f67),
  /** An asteroid's rock, colored per vertex. */
  rock: std(0xffffff, { vertexColors: true }),
  rockSide: std(0x6a635c),
  boulder: std(0x6f6862),
  plaza: std(0x2b313a, { metalness: 0.3, roughness: 0.6 }),
  plazaEdge: new THREE.MeshBasicMaterial({ color: 0x58d8f0 }),
  pole: std(0x9aa3ad, { metalness: 0.6, roughness: 0.35 }),
  bldEdge: new THREE.LineBasicMaterial({ color: 0x7fe9ff, transparent: true, opacity: 0.5 }),
  bldBand: new THREE.MeshBasicMaterial({ color: 0x7fe9ff }),
}

/** Dark metal towers, glowing blue while read, amber while edited, red in conflict, and bright cyan while selected. */
export const BUILDINGS: BuildingMats = {
  rest: std(0x4b5666, { emissive: 0x15304c, emissiveIntensity: 0.55, metalness: 0.3, roughness: 0.55, ...offset }),
  read: std(0x6fa3e8, { emissive: 0x2f6fc8, emissiveIntensity: 0.75, ...offset }),
  edit: std(0xf0c060, { emissive: 0xb07a10, emissiveIntensity: 0.75, ...offset }),
  conflict: std(0xf08a7e, { emissive: 0xc0352a, emissiveIntensity: 0.85, ...offset }),
  selected: std(0xd8fbff, { emissive: 0x3fd9f0, emissiveIntensity: 0.6, ...offset }),
}

/** The sci-fi theme's shared geometries. Unit-sized ones are stretched by their meshes. */
export const SG = {
  /** A hexagonal tower, one unit wide and tall. */
  hex: new THREE.CylinderGeometry(0.5, 0.5, 1, 6),
  /** A hexagonal band with no caps, for the lit rings around towers. */
  hexBand: new THREE.CylinderGeometry(0.5, 0.5, 1, 6, 1, true),
  pole: new THREE.CylinderGeometry(0.025, 0.035, 0.85, 6),
  lamp: new THREE.SphereGeometry(0.07, 8, 6),
  halo: new THREE.SphereGeometry(0.17, 8, 6),
  boulder: new THREE.IcosahedronGeometry(1, 0),
}
const hexEdges = new THREE.EdgesGeometry(SG.hex)
export const SG_EDGES = { hex: hexEdges }

markShared([...Object.values(S), ...Object.values(BUILDINGS), ...Object.values(SG), hexEdges])
