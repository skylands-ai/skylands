type Attrs = Record<string, string | ((ev: Event) => void) | null | undefined>
type Kid = Node | string | null | undefined

/** Builds an element. "text" sets textContent, "class" the class name, and "on*" keys add listeners. */
export function el<K extends keyof HTMLElementTagNameMap>(tag: K, attrs?: Attrs, kids?: Kid[]): HTMLElementTagNameMap[K] {
  const n = document.createElement(tag)
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined) continue
      if (typeof v === 'function') n.addEventListener(k.slice(2), v)
      else if (k === 'text') n.textContent = v
      else if (k === 'class') n.className = v
      else n.setAttribute(k, v)
    }
  }
  for (const c of kids ?? []) {
    if (c) n.appendChild(typeof c === 'string' ? document.createTextNode(c) : c)
  }
  return n
}

/** Reads a CSS custom property from the document, so the scene follows the theme tokens. */
export function css(name: string, fallback: string): string {
  let v = ''
  try {
    v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  } catch {
    v = ''
  }
  return v || fallback
}

/**
 * The control that has the keyboard, when it carries a data-keep key: a
 * control drawn again under the same key, such as a reply box that keeps
 * its draft, gets the keyboard back with its caret where it was.
 */
export function heldKeyboard(root: HTMLElement): (() => void) | null {
  const a = document.activeElement
  if (!(a instanceof HTMLElement) || !root.contains(a) || !a.dataset.keep) return null
  const key = a.dataset.keep
  const field = a instanceof HTMLInputElement || a instanceof HTMLTextAreaElement ? a : null
  // Some inputs, such as a number's, have no caret to read.
  let caret: [number, number, 'forward' | 'backward' | 'none'] | null = null
  try {
    if (field && field.selectionStart !== null && field.selectionEnd !== null) caret = [field.selectionStart, field.selectionEnd, field.selectionDirection ?? 'none']
  } catch {
    caret = null
  }
  const top = a.scrollTop
  return () => {
    if (a.isConnected) return
    const next = root.querySelector<HTMLElement>(`[data-keep="${CSS.escape(key)}"]`)
    if (!next || next.matches(':disabled')) return
    next.focus({ preventScroll: true })
    if (caret && (next instanceof HTMLInputElement || next instanceof HTMLTextAreaElement)) {
      try {
        next.setSelectionRange(...caret)
      } catch {
        // The new control has no caret either.
      }
    }
    next.scrollTop = top
  }
}

export function byId<T extends HTMLElement>(id: string): T {
  const n = document.getElementById(id)
  if (!n) throw new Error(`missing element #${id}`)
  return n as T
}

export const reduceMotion = !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches)
