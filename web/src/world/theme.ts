import * as THREE from 'three'
import type { WorldThemeId } from '../worldthemes'
import type { Airship, Provider } from './airship'
import type { Placement } from './placement'
import type { Crystal } from './shapes'
import type { TerraceSpec } from './terrace'

/**
 * The materials a building wears for each state. The world swaps a
 * building's material between them, and tells the states apart by which one
 * it wears, so each theme keeps one shared set for all its buildings.
 */
export interface BuildingMats {
  rest: THREE.MeshStandardMaterial
  /** An agent reads the file. */
  read: THREE.MeshStandardMaterial
  /** An agent edits the file. */
  edit: THREE.MeshStandardMaterial
  /** Two sessions are on the file at once. */
  conflict: THREE.MeshStandardMaterial
  /** The selected building, whose glow the world makes breathe. */
  selected: THREE.MeshStandardMaterial
}

export interface Citadel {
  group: THREE.Group
  /** The world's NATS server. Every tether ends here. */
  core: Crystal
  /** Lit and pulsing while anything in the world waits on a person. */
  beacon: THREE.Mesh
  halo: THREE.Mesh<THREE.SphereGeometry, THREE.MeshBasicMaterial>
  light: THREE.PointLight
  /** The selection ring around the citadel. It turns slowly while visible. */
  ring: THREE.Group
  /** Height above the group where its label floats. */
  labelY: number
  /** Moves what the citadel animates on its own, once a frame. */
  animate?(time: number, dt: number): void
}

export interface IslandMeshes {
  group: THREE.Group
  /** The daemon as the citadel last saw it: lit while reachable, grey while it can't tell. */
  core: Crystal
  /** The selection ring around the island. It turns slowly while visible. */
  ring: THREE.Group
  /** Moves what the island animates on its own, once a frame. */
  animate?(time: number, dt: number): void
}

export interface TerraceMeshes {
  group: THREE.Group
  /** One building per file, in the order of the spec's files. */
  blds: THREE.Mesh[]
  /** Moves the terrace's boundary, which shows whether the terrace is selected. */
  animate(time: number, selected: boolean): void
}

/** How the sky looks in a theme that does not follow the page's light or dark colors. */
export interface SkyLook {
  top: number
  horizon: number
  fog: number
  fogNear: number
  fogFar: number
  /** Star opacity, 0 to 1. */
  stars: number
  /** Stars all around, below the horizon too, as in space. */
  starsAround: boolean
  clouds: boolean
  hemiSky: number
  hemiGround: number
  hemiIntensity: number
  sun: number
  sunIntensity: number
}

/**
 * Scenery that crosses the view now and then and carries no data: a bird
 * in the default theme, a shooting star in sci-fi. The world decides when;
 * the visitor decides how it looks and where across the view it goes.
 */
export interface Visitor {
  object: THREE.Object3D
  /** How long one pass takes, in seconds. */
  duration: number
  /** Starts a pass across what camera sees. */
  begin(camera: THREE.PerspectiveCamera): void
  /** Moves it to t, from 0 to 1, along the pass. time is the world's clock, in seconds. */
  step(t: number, time: number, camera: THREE.PerspectiveCamera): void
}

/**
 * One look for the world. Every object it builds sits where the shared
 * layout puts it and carries the same pick data, so changing themes changes
 * only how the world is drawn.
 */
export interface WorldTheme {
  id: WorldThemeId
  buildCitadel(): Citadel
  /** One host's island. Its walkable top is at y = 0. */
  buildIsland(host: string, p: Placement): IslandMeshes
  buildTerrace(spec: TerraceSpec, seed: number): TerraceMeshes
  /** An airship for an agent from provider. Its underside, where the beam starts, is at y = -0.6. */
  airship(prov: Provider): Airship
  /** Null: the sky follows the page's light or dark colors. */
  sky: SkyLook | null
  /** What passes through the sky now and then, if anything. */
  visitor?(): Visitor
}

/** Marks geometries and materials shared by many objects, which disposeTree leaves alone. */
export function markShared(things: (THREE.BufferGeometry | THREE.Material)[]): void {
  for (const t of things) t.userData.shared = true
}

/** Takes an object out of the scene and frees the geometries and materials it owns. */
export function disposeTree(root: THREE.Object3D): void {
  root.parent?.remove(root)
  root.traverse((o) => {
    const m = o as THREE.Mesh
    if (m.geometry && !m.geometry.userData.shared) m.geometry.dispose()
    const mats = Array.isArray(m.material) ? m.material : m.material ? [m.material] : []
    for (const mat of mats) if (!mat.userData.shared) mat.dispose()
  })
}
