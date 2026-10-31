import { el } from './dom'
import { islandCounts, isStale } from './status'
import type { State } from './store'

/** The breadcrumb, the connected-islands count, and the citadel link pill in the top bar. */
export function renderHeader(crumbs: HTMLElement, connected: HTMLElement, link: HTMLElement, s: State, overview: () => void): void {
  crumbs.textContent = ''
  if (s.selected === 'cit') {
    crumbs.appendChild(el('span', { class: 'btn small', 'aria-current': 'page', text: 'World' }))
  } else {
    crumbs.appendChild(el('button', { type: 'button', class: 'btn small', text: 'World', onclick: overview }))
    crumbs.appendChild(el('span', { class: 'sep', 'aria-hidden': 'true', text: '/' }))
    crumbs.appendChild(el('span', { style: 'font-weight:600', 'aria-current': 'page', text: s.selected }))
  }
  const { reachable, total } = islandCounts(s)
  const counts = total ? `${reachable} of ${total} islands connected` : 'No islands yet'
  connected.textContent = isStale(s) && total ? `${counts}, as last heard` : counts

  const [text, color] =
    s.link === 'connected'
      ? ['Citadel connected', 'var(--ok)']
      : s.link === 'lost'
        ? ['Citadel unreachable, retrying', 'var(--bad)']
        : ['Connecting to citadel', 'var(--warn)']
  link.textContent = ''
  link.append(el('span', { class: 'dot', style: `background:${color}` }), text)
}
