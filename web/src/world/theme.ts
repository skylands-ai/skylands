import * as THREE from 'three'
import type { Airship, Provider } from './airship'
import type { Crystal } from './shapes'

/**
 * The materials a building wears for each state. The world dresses a
 * building in them, swaps between them, and tells the states apart by which
 * one it wears, so each theme keeps one shared set for all its buildings.
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

/**
 * The ring around the citadel or an island that shows it is selected. The
 * world shows it only while it is, and moves it only then.
 */
export interface SelectionRing {
  group: THREE.Group
  /** Moves the ring, once a frame while it is shown. */
  animate(time: number, dt: number): void
}

/** The citadel, drawn around its own origin, which the world floats over the middle of the world. */
export interface Citadel {
  group: THREE.Group
  /** The world's NATS server. Every tether ends here. */
  core: Crystal
  /** Its selection ring, in its group. */
  ring: SelectionRing
  /** Shown, swelling and fading, while anything in the world waits on a person. */
  halo: THREE.Mesh<THREE.SphereGeometry, THREE.MeshBasicMaterial>
  /** The beacon's light, brighter while anything in the world waits on a person. */
  light: THREE.PointLight
  /** Height above the group where its label floats. */
  labelY: number
  /** Moves what the citadel animates on its own, once a frame. */
  animate?(time: number, dt: number): void
}

/**
 * What a theme draws an island from: its size and the seed and phase that
 * keep its look and rhythm its own. Nothing in it names the host.
 */
export interface IslandShape {
  /** The radius of its walkable top. */
  R: number
  /** How far its rock reaches below the top. */
  depth: number
  seed: number
  /** Where in a cycle its own rhythms start, in radians. */
  phase: number
}

/**
 * One host's island, drawn around its own origin with its walkable top at
 * y = 0. The world moves it to its place. A child of the group with a
 * userData.scenery pad is scenery, which the world hides where a terrace
 * covers it, with that much room around the terrace.
 */
export interface IslandMeshes {
  group: THREE.Group
  /** The daemon as the citadel last saw it: lit while reachable, grey while it can't tell. */
  core: Crystal
  /** Its selection ring, in its group. */
  ring: SelectionRing
  /** Moves what the island animates on its own, once a frame. */
  animate?(time: number, dt: number): void
}

/**
 * What a theme draws a terrace from: the size of its plaza and its repo's
 * color. Nothing in it names the repo or its files.
 */
export interface TerracePlot {
  sx: number
  sz: number
  /** The repo's color, the same everywhere the repo appears. */
  color: number
}

/**
 * A terrace's plaza and its boundary, drawn around the plaza's center. The
 * world moves it to its place on the island and stands the buildings on it.
 */
export interface TerraceMeshes {
  group: THREE.Group
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
 * One look for the world. A theme draws each thing around its own origin
 * from a few numbers, and the world places it, tags it with what a click on
 * it means, and shows its selection ring while it is selected, so changing
 * themes changes only how the world is drawn. Nothing a theme is given
 * names a host, a repo, or a file.
 */
export interface WorldTheme {
  /** A built-in theme's id, or a theme pack's. */
  id: string
  buildCitadel(): Citadel
  buildIsland(shape: IslandShape): IslandMeshes
  buildTerrace(plot: TerracePlot): TerraceMeshes
  /**
   * One file's building, height tall. Its geometry is a unit block, which
   * the world stretches to the building's lot and stands there, dressed in
   * buildings.rest. Anything on it is a child, scaled back by that stretch.
   */
  building(height: number): THREE.Mesh
  buildings: BuildingMats
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
