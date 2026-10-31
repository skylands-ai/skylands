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

export function byId<T extends HTMLElement>(id: string): T {
  const n = document.getElementById(id)
  if (!n) throw new Error(`missing element #${id}`)
  return n as T
}

export const reduceMotion = !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches)
