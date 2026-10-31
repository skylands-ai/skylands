import { el } from '../dom'
import type { Status } from '../world/world'

/** A panel heading with an optional status pill and a one-line description. */
export function header(title: string, kind: string | null, status: Status | null): HTMLElement {
  return el('div', { class: 'ph' }, [
    el('div', { style: 'flex:1;min-width:0' }, [
      el('div', { style: 'display:flex;flex-wrap:wrap;align-items:center;gap:10px' }, [
        el('h2', { text: title }),
        status ? el('span', { class: 'pill' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]) : null,
      ]),
      kind ? el('p', { class: 'muted', style: 'margin:4px 0 0;font-size:13px', text: kind }) : null,
    ]),
  ])
}

/** Label and value rows. */
export function factsBox(rows: [string, string][]): HTMLElement {
  return el(
    'div',
    { class: 'facts' },
    rows.map(([k, v]) => el('div', { class: 'fact' }, [el('span', { class: 'muted', text: k }), el('b', { text: v })])),
  )
}

export function section(title: string, kids: (Node | null)[]): HTMLElement {
  return el('section', { class: 'sec', 'aria-label': title }, [el('h3', { text: title }), ...kids])
}

export function note(text: string): HTMLElement {
  return el('p', { class: 'note', text })
}

export function hint(text: string): HTMLElement {
  return el('p', { class: 'muted', style: 'margin:0;font-size:13px', text })
}
