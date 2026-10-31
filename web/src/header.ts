import { el } from './dom'
import { base, sortedFiles } from './files'
import { islandCounts, isStale } from './status'
import { level, type State } from './store'

export interface NavActions {
  overview(): void
  select(host: string): void
  land(host: string, terrace: string): void
  selectAgent(host: string, terrace: string, agent: string): void
}

function crumbBtn(text: string, fn: () => void): HTMLElement {
  return el('button', { type: 'button', class: 'btn small', text, onclick: fn })
}

function current(text: string): HTMLElement {
  return el('span', { style: 'font-weight:600', 'aria-current': 'page', text })
}

function sep(): HTMLElement {
  return el('span', { class: 'sep', 'aria-hidden': 'true', text: '/' })
}

/** The breadcrumb, the connected-islands count, and the citadel link pill in the top bar. */
export function renderHeader(crumbs: HTMLElement, connected: HTMLElement, link: HTMLElement, s: State, nav: NavActions): void {
  crumbs.textContent = ''
  const l = level(s)
  if (l === 'world') {
    crumbs.appendChild(el('span', { class: 'btn small', 'aria-current': 'page', text: 'World' }))
  } else {
    crumbs.appendChild(crumbBtn('World', nav.overview))
    crumbs.appendChild(sep())
    const host = s.selected
    if (l === 'island') crumbs.appendChild(current(host))
    else {
      crumbs.appendChild(crumbBtn(host, () => nav.select(host)))
      crumbs.appendChild(sep())
      const repo = s.terrace ? s.repos[s.terrace] : undefined
      const tname = repo?.name ?? 'terrace'
      if (l === 'terrace') crumbs.appendChild(current(tname))
      else {
        const tid = s.terrace as string
        crumbs.appendChild(crumbBtn(tname, () => nav.land(host, tid)))
        crumbs.appendChild(sep())
        if (l === 'agent') {
          const ag = s.agent ? s.sessions[s.agent] : undefined
          crumbs.appendChild(current(ag?.agentName ?? ag?.agent ?? 'agent'))
        } else {
          const f = repo && s.file !== null ? sortedFiles(repo)[s.file] : undefined
          crumbs.appendChild(current(f ? base(f.path) : 'file'))
        }
      }
    }
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
