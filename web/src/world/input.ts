import type { CameraRig } from './camera'

export interface InputHandlers {
  /** A click or tap that did not drag. deep is true for a double-click. */
  onPick(clientX: number, clientY: number, deep: boolean): void
  /** Whether something pickable is under the pointer, for the cursor. */
  hasTarget(clientX: number, clientY: number): boolean
}

interface Drag {
  x: number
  y: number
  moved: number
  pinch?: number
  radius?: number
}

/** Drag orbits, scroll or pinch zooms, and a still click picks. */
export function attachPointerInput(canvas: HTMLCanvasElement, rig: CameraRig, h: InputHandlers): void {
  const pointers = new Map<number, { x: number; y: number }>()
  let drag: Drag | null = null

  canvas.addEventListener('pointerdown', (e) => {
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    canvas.setPointerCapture?.(e.pointerId)
    if (pointers.size === 1) drag = { x: e.clientX, y: e.clientY, moved: 0 }
    else if (pointers.size === 2) {
      const [a, b] = [...pointers.values()]
      drag = { x: 0, y: 0, pinch: Math.hypot(a.x - b.x, a.y - b.y), radius: rig.want.radius, moved: 99 }
    }
  })
  canvas.addEventListener('pointermove', (e) => {
    if (pointers.has(e.pointerId)) pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (drag && pointers.size === 2 && drag.pinch && drag.radius) {
      const [a, b] = [...pointers.values()]
      const d = Math.hypot(a.x - b.x, a.y - b.y)
      if (d > 0) rig.zoomTo((drag.radius * drag.pinch) / d)
      return
    }
    if (drag && pointers.size === 1) {
      const dx = e.clientX - drag.x
      const dy = e.clientY - drag.y
      drag.moved += Math.abs(dx) + Math.abs(dy)
      drag.x = e.clientX
      drag.y = e.clientY
      rig.orbitBy(dx, dy)
      return
    }
    if (e.pointerType === 'mouse') canvas.style.cursor = h.hasTarget(e.clientX, e.clientY) ? 'pointer' : 'grab'
  })
  const endPointer = (e: PointerEvent): void => {
    const wasClick = drag && !drag.pinch && drag.moved < 6 && pointers.size === 1
    pointers.delete(e.pointerId)
    if (wasClick && e.type === 'pointerup') h.onPick(e.clientX, e.clientY, false)
    if (pointers.size === 0) drag = null
  }
  canvas.addEventListener('pointerup', endPointer)
  canvas.addEventListener('pointercancel', endPointer)
  canvas.addEventListener('dblclick', (e) => {
    e.preventDefault()
    h.onPick(e.clientX, e.clientY, true)
  })
  canvas.addEventListener(
    'wheel',
    (e) => {
      e.preventDefault()
      rig.zoomTo(rig.want.radius * Math.exp(e.deltaY * 0.0012))
    },
    { passive: false },
  )
}
