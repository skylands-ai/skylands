import * as THREE from 'three'
import { reduceMotion } from '../dom'
import { disposeTree, type Visitor } from './theme'

/** Seconds until the first visitor after the world starts or changes theme, at random between these. */
const FIRST: [number, number] = [12, 30]
/** Seconds between visitors after that, at random between these. */
const EVERY: [number, number] = [45, 120]

const between = ([lo, hi]: [number, number]): number => lo + Math.random() * (hi - lo)

/**
 * Sends the theme's visitor across the view every once in a while. It is
 * scenery, so it never runs when the person asked for reduced motion.
 */
export class Visitors {
  private visitor: Visitor | null = null
  /** Seconds until the next pass, while none is under way. */
  private wait = between(FIRST)
  /** How far along the current pass is, or -1 between passes. */
  private t = -1

  constructor(private readonly scene: THREE.Scene) {}

  /** Takes a theme's visitor, or none, in place of the current one. */
  set(v: Visitor | null): void {
    if (this.visitor) disposeTree(this.visitor.object)
    this.visitor = v
    this.t = -1
    this.wait = between(FIRST)
    if (v) {
      v.object.visible = false
      this.scene.add(v.object)
    }
  }

  /** Sends one across now, for debugging and screenshots. */
  sendNow(): void {
    if (this.t < 0) this.wait = 0
  }

  update(dt: number, time: number, camera: THREE.PerspectiveCamera): void {
    const v = this.visitor
    if (!v || reduceMotion) return
    if (this.t < 0) {
      this.wait -= dt
      if (this.wait > 0) return
      v.begin(camera)
      v.object.visible = true
      this.t = 0
    }
    this.t += dt / v.duration
    if (this.t >= 1) {
      v.object.visible = false
      this.t = -1
      this.wait = between(EVERY)
      return
    }
    v.step(this.t, time, camera)
  }
}

/**
 * What the camera sees at distance in front of it: the point there on its
 * line of sight, its right and up directions, and half the width and
 * height of the view at that distance.
 */
export function viewAt(camera: THREE.PerspectiveCamera, distance: number): { center: THREE.Vector3; right: THREE.Vector3; up: THREE.Vector3; halfW: number; halfH: number } {
  camera.updateMatrixWorld()
  const e = camera.matrixWorld.elements
  const right = new THREE.Vector3(e[0], e[1], e[2]).normalize()
  const up = new THREE.Vector3(e[4], e[5], e[6]).normalize()
  const forward = new THREE.Vector3(-e[8], -e[9], -e[10]).normalize()
  const halfH = distance * Math.tan(THREE.MathUtils.degToRad(camera.fov / 2))
  return { center: camera.position.clone().addScaledVector(forward, distance), right, up, halfW: halfH * camera.aspect, halfH }
}
