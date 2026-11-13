import * as THREE from 'three'
import { css, reduceMotion } from '../../../dom'
import { markShared, type SelectionRing } from '../../theme'

/** One material for every ring, so their color changes in one place. */
const RING = new THREE.MeshBasicMaterial({ color: 0xffffff, transparent: true, opacity: 0.9 })
markShared([RING])

/**
 * The selection ring: a circle of short arcs lying flat, so a slow turn is
 * visible, which a plain torus would hide. A pastel hue sweeps round every
 * fifteen seconds or so, which keeps the ring lively without shouting.
 */
export function selectionRing(radius: number, tube: number): SelectionRing {
  const g = new THREE.Group()
  const n = 16
  const arc = ((Math.PI * 2) / n) * 0.58
  for (let i = 0; i < n; i++) {
    const seg = new THREE.Mesh(new THREE.TorusGeometry(radius, tube, 6, 10, arc), RING)
    seg.rotation.z = (i / n) * Math.PI * 2
    seg.raycast = () => {}
    g.add(seg)
  }
  g.rotation.x = Math.PI / 2
  return {
    group: g,
    animate(time, dt) {
      if (reduceMotion) {
        RING.color.set(css('--accent', '#2D5BC7'))
        return
      }
      RING.color.setHSL((time * 0.065) % 1, 0.7, 0.72)
      g.rotation.z += dt * 0.35
    },
  }
}
