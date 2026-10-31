import * as THREE from 'three'
import { el } from '../dom'

const tmp = new THREE.Vector3()

/** DOM labels projected from 3D positions. They stay crisp and work as real buttons. */
export class Labels {
  constructor(
    private readonly root: HTMLElement,
    private readonly stage: HTMLElement,
    private readonly camera: THREE.Camera,
  ) {}

  /** Makes a label button for one thing in the world. The caller fills and places it. */
  create(onClick: () => void): HTMLButtonElement {
    const b = el('button', { type: 'button', class: 'label', onclick: onClick })
    this.root.appendChild(b)
    return b
  }

  /** Moves node to the screen position of a world point, raised by dy. Returns false when off screen. */
  placeAt(node: HTMLElement, world: THREE.Vector3, dy: number, below = false): boolean {
    tmp.copy(world)
    tmp.y += dy
    tmp.project(this.camera)
    if (tmp.z > 1 || tmp.z < -1 || Math.abs(tmp.x) > 1.2 || Math.abs(tmp.y) > 1.2) {
      this.hide(node)
      return false
    }
    const x = ((tmp.x + 1) / 2) * this.stage.clientWidth
    const y = ((1 - tmp.y) / 2) * this.stage.clientHeight
    node.style.transform = `translate(${x.toFixed(1)}px,${y.toFixed(1)}px) ${below ? 'translate(-50%,6px)' : 'translate(-50%,-100%)'}`
    return true
  }

  hide(node: HTMLElement): void {
    node.style.transform = 'translate(-9999px,-9999px)'
  }
}
