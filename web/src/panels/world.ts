import { el } from '../dom'
import type { ServerInfo } from '../protocol'
import { citadelStatus, islandCounts, islandStatus, isStale, shortId } from '../status'
import { islandList, waitingCount, type State } from '../store'
import { WORLD_THEMES, type WorldThemeId } from '../worldthemes'
import { factsBox, header, hint, note, section } from './parts'

function natsFacts(s: ServerInfo | null, link: State['link'], daemons: string): [string, string][] {
  if (link !== 'connected') return [['Connected through', 'Unknown while the citadel is unreachable']]
  if (!s) return [['Connected through', 'Waiting for the citadel to report']]
  if (!s.connected) return [['Connected through', s.url ? `Trying ${s.url}` : 'Trying to connect']]
  const servers = s.servers ?? 1
  return [
    ['Server name', s.name ?? 'unnamed'],
    ['Server ID', s.id ? shortId(s.id) : 'unknown'],
    ['NATS Server version', s.version ?? 'unknown'],
    ['Cluster', s.cluster ? `${s.cluster}, ${servers} server${servers === 1 ? '' : 's'}` : 'None, single server'],
    ['JetStream domain', s.domain ?? 'none'],
    ['Connected through', s.url ?? 'unknown'],
    ['Daemons connected', daemons],
  ]
}

/**
 * The switch between the world's themes. A theme changes how the 3D world
 * is drawn in this browser, and nothing else on the page.
 */
function themeSwitch(current: WorldThemeId, choose: (id: WorldThemeId) => void): HTMLElement {
  const blurb = WORLD_THEMES.find((t) => t.id === current)?.blurb ?? ''
  return section('World theme', [
    el(
      'div',
      { class: 'modes', role: 'group', 'aria-label': 'World theme' },
      WORLD_THEMES.map((t) =>
        el('button', { type: 'button', class: 'btn small', 'aria-pressed': t.id === current ? 'true' : 'false', text: t.name, onclick: () => choose(t.id) }),
      ),
    ),
    hint(`${blurb} Only the world changes, and only in this browser.`),
  ])
}

/** The world-level panel: the citadel itself, the world's theme, its islands, and the NATS server it is on. */
export function renderWorldPanel(panel: HTMLElement, s: State, select: (id: string) => void, setTheme: (id: WorldThemeId) => void): void {
  panel.textContent = ''
  const stale = isStale(s)
  const { reachable, total } = islandCounts(s)
  const counts = `${reachable} of ${total}${stale && total ? ', as last seen' : ''}`
  // The same count as the Inbox badge and the tab title.
  const waiting = waitingCount(s)
  panel.appendChild(header('Citadel', 'Web app and observer for this world', citadelStatus(s)))
  panel.appendChild(
    note(
      'Every island’s daemon keeps its own event log and syncs it to the world’s NATS server, where every tether ends at the citadel. Decisions you make here travel back down the tethers.',
    ),
  )
  panel.appendChild(
    factsBox([
      ['Islands connected', counts],
      ['Waiting on you', `${waiting}${stale && waiting ? ', as last seen' : ''}`],
    ]),
  )
  panel.appendChild(themeSwitch(s.worldTheme, setTheme))

  const islands = islandList(s)
  if (islands.length) {
    panel.appendChild(
      section(
        'Islands',
        islands.map((i) => {
          const st = islandStatus(i, stale)
          return el('button', { type: 'button', class: 'row', style: 'text-align:left;width:100%', onclick: () => select(i.host) }, [
            el('span', { class: 'dot', style: `width:10px;height:10px;background:${st[1]}` }),
            el('span', { class: 'grow' }, [
              el('span', { style: 'font-weight:600', text: i.host }),
              el('span', { class: 'sub', text: `${st[0]}, ${i.daemon.os}/${i.daemon.arch}, skylands ${i.daemon.version}` }),
            ]),
          ])
        }),
      ),
    )
  } else {
    panel.appendChild(note('No islands yet. Start a daemon on a machine and its island rises here.'))
  }

  panel.appendChild(
    section('NATS', [
      hint('The NATS server this citadel is connected to, as the server describes itself. Every tether ends at it.'),
      factsBox(natsFacts(s.server, s.link, counts)),
    ]),
  )
}
