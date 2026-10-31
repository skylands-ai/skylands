import { el } from './dom'
import { base, sortedFiles } from './files'
import { placeIcon, type Place } from './panels/parts'
import { agentLabel, islandCounts, isStale } from './status'
import { level, type State } from './store'

export interface NavActions {
  overview(): void
  select(host: string): void
  land(host: string, terrace: string): void
  selectAgent(host: string, terrace: string, agent: string): void
}

function crumbBtn(text: string, fn: () => void, place?: Place): HTMLElement {
  return el('button', { type: 'button', class: 'btn small', onclick: fn }, [place ? placeIcon(place) : null, text])
}

function current(text: string, place?: Place): HTMLElement {
  return el('span', { style: 'font-weight:600;display:inline-flex;align-items:center;gap:6px', 'aria-current': 'page' }, [place ? placeIcon(place) : null, text])
}

function sep(): HTMLElement {
  return el('span', { class: 'sep', 'aria-hidden': 'true', text: '/' })
}

/** The breadcrumb, the connected-islands count, and the citadel link pill in the top bar. */
export function renderHeader(crumbs: HTMLElement, connected: HTMLElement, link: HTMLElement, s: State, nav: NavActions): void {
  crumbs.textContent = ''
  const l = level(s)
  if (l === 'world') {
    const world = crumbBtn('World', nav.overview, 'world')
    world.setAttribute('aria-current', 'page')
    crumbs.appendChild(world)
  } else {
    crumbs.appendChild(crumbBtn('World', nav.overview, 'world'))
    crumbs.appendChild(sep())
    const host = s.selected
    if (l === 'island') crumbs.appendChild(current(host, 'island'))
    else {
      crumbs.appendChild(crumbBtn(host, () => nav.select(host), 'island'))
      crumbs.appendChild(sep())
      const repo = s.terrace ? s.repos[s.terrace] : undefined
      const tname = repo?.name ?? 'terrace'
      if (l === 'terrace') crumbs.appendChild(current(tname, 'repo'))
      else {
        const tid = s.terrace as string
        crumbs.appendChild(crumbBtn(tname, () => nav.land(host, tid), 'repo'))
        crumbs.appendChild(sep())
        if (l === 'agent') {
          const ag = s.agent ? s.sessions[s.agent] : undefined
          crumbs.appendChild(current(ag ? agentLabel(ag.agent) : 'agent'))
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
