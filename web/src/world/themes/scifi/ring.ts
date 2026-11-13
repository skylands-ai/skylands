import * as THREE from 'three'
import { reduceMotion } from '../../../dom'
import { markShared, type SelectionRing } from '../../theme'

/** The circle: a faint line in the cyan of the station's lit trim. */
const LINE = new THREE.MeshBasicMaterial({ color: 0x6fe6ff, transparent: true, opacity: 0.45, depthWrite: false })
/** The brackets and their markers, brighter, seen from above or below. One material for every ring, so they breathe together. */
const BRACKET = new THREE.MeshBasicMaterial({ color: 0xa8f3ff, transparent: true, opacity: 0.95, side: THREE.DoubleSide })
markShared([LINE, BRACKET])

/** How much of the circle each of the four brackets covers. */
const BRACKET_ARC = Math.PI / 4

const noRay = (): void => {}

/**
 * The selection ring in the sci-fi theme, a targeting reticle: a thin
 * circle with four brackets on it, each with a marker pointing in at what
 * is selected. The brackets turn slowly and their glow breathes.
 */
export function reticle(radius: number, tube: number): SelectionRing {
  const g = new THREE.Group()
  const circle = new THREE.Mesh(new THREE.TorusGeometry(radius, tube * 0.35, 4, 72), LINE)
  circle.raycast = noRay
  g.add(circle)

  const brackets = new THREE.Group()
  const arcGeo = new THREE.TorusGeometry(radius, tube, 6, 12, BRACKET_ARC)
  // A flat triangle pointing along +x, from a three-sided circle.
  const markGeo = new THREE.CircleGeometry(tube * 3.2, 3)
  for (let i = 0; i < 4; i++) {
    const a = (i / 4) * Math.PI * 2
    const arc = new THREE.Mesh(arcGeo, BRACKET)
    arc.rotation.z = a - BRACKET_ARC / 2
    const mark = new THREE.Mesh(markGeo, BRACKET)
    mark.position.set(Math.cos(a) * (radius - tube * 5), Math.sin(a) * (radius - tube * 5), 0)
    mark.rotation.z = a + Math.PI
    arc.raycast = noRay
    mark.raycast = noRay
    brackets.add(arc, mark)
  }
  g.add(brackets)
  g.rotation.x = Math.PI / 2
  return {
    group: g,
    animate(time, dt) {
      if (reduceMotion) {
        BRACKET.opacity = 0.95
        return
      }
      brackets.rotation.z += dt * 0.35
      BRACKET.opacity = 0.8 + 0.15 * Math.sin(time * 2.6)
    },
  }
}
