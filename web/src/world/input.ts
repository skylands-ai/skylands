import type { CameraRig } from './camera'

export interface InputHandlers {
  /** A click or tap that did not drag. deep is true for a double-click. */
  onPick(clientX: number, clientY: number, deep: boolean): void
  /** Whether something pickable is under the pointer, for the cursor. */
  hasTarget(clientX: number, clientY: number): boolean
}

interface Drag {
  /** Where the pointer, or the midpoint of two touches, last was. */
  x: number
  y: number
  moved: number
  /** Whether letting go without moving picks. Only a primary press does. */
  pick: boolean
  /** A right- or middle-button drag pans rather than orbits. */
  pan: boolean
  /** The gap between two touches when the pinch began, and the radius then. */
  pinch?: number
  radius?: number
}

/**
 * Drag orbits; right-drag, middle-drag, Shift-, Ctrl-, or Cmd-drag, or two
 * fingers pan; scroll or pinch zooms; and a still click picks.
 */
export function attachPointerInput(canvas: HTMLCanvasElement, rig: CameraRig, h: InputHandlers): void {
  const pointers = new Map<number, { x: number; y: number }>()
  let drag: Drag | null = null

  const two = (): { x: number; y: number; gap: number } => {
    const [a, b] = [...pointers.values()]
    return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2, gap: Math.hypot(a.x - b.x, a.y - b.y) }
  }
  // Starts the gesture over from the pointers now down, so a finger landing
  // or lifting mid-gesture does not make the camera jump.
  const restart = (pick: boolean, pan: boolean): void => {
    if (pointers.size === 1) {
      const [p] = pointers.values()
      drag = { x: p.x, y: p.y, moved: 0, pick, pan }
    } else if (pointers.size === 2) {
      const t = two()
      drag = { x: t.x, y: t.y, moved: 0, pick: false, pan: true, pinch: t.gap, radius: rig.want.radius }
    } else drag = null
  }
  const hover = (e: PointerEvent): void => {
    if (e.pointerType === 'mouse') canvas.style.cursor = h.hasTarget(e.clientX, e.clientY) ? 'pointer' : 'grab'
  }

  canvas.addEventListener('pointerdown', (e) => {
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    canvas.setPointerCapture?.(e.pointerId)
    restart(e.button === 0, e.button === 1 || e.button === 2)
    if (drag?.pan && e.pointerType === 'mouse') canvas.style.cursor = 'move'
  })
  canvas.addEventListener('pointermove', (e) => {
    const p = pointers.get(e.pointerId)
    if (!p) {
      hover(e)
      return
    }
    p.x = e.clientX
    p.y = e.clientY
    if (!drag) return
    if (pointers.size === 2 && drag.pinch && drag.radius) {
      const t = two()
      if (t.gap > 0) rig.zoomTo((drag.radius * drag.pinch) / t.gap)
      rig.panBy(t.x - drag.x, t.y - drag.y)
      drag.x = t.x
      drag.y = t.y
      return
    }
    if (pointers.size !== 1) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    drag.moved += Math.abs(dx) + Math.abs(dy)
    drag.x = e.clientX
    drag.y = e.clientY
    const pan = drag.pan || e.shiftKey || e.ctrlKey || e.metaKey
    if (pan) rig.panBy(dx, dy)
    else rig.orbitBy(dx, dy)
    if (e.pointerType === 'mouse') canvas.style.cursor = pan ? 'move' : 'grab'
  })
  const endPointer = (e: PointerEvent): void => {
    const wasClick = drag?.pick && drag.moved < 6 && pointers.size === 1
    pointers.delete(e.pointerId)
    if (wasClick && e.type === 'pointerup') h.onPick(e.clientX, e.clientY, false)
    // A finger left on the glass after a pinch orbits, and does not pick.
    restart(false, false)
    if (!drag) hover(e)
  }
  canvas.addEventListener('pointerup', endPointer)
  canvas.addEventListener('pointercancel', endPointer)
  canvas.addEventListener('dblclick', (e) => {
    e.preventDefault()
    h.onPick(e.clientX, e.clientY, true)
  })
  // Right-drag pans, so the browser's menu stays shut.
  canvas.addEventListener('contextmenu', (e) => e.preventDefault())
  // Middle-drag pans, so the browser does not start autoscrolling.
  canvas.addEventListener('mousedown', (e) => {
    if (e.button === 1) e.preventDefault()
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
