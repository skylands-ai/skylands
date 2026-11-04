import { el } from '../dom'
import type { ServerInfo, WorldInfo } from '../protocol'
import { citadelStatus, islandCounts, islandStatus, isStale, shortId } from '../status'
import { islandList, waitingCount, type State } from '../store'
import { WORLD_THEMES, type WorldThemeId } from '../worldthemes'
import { copyButton, factsBox, header, hint, note, section } from './parts'

function natsFacts(s: ServerInfo | null, link: State['link']): [string, string][] {
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

/**
 * How to add a host to this world: the install command, with the world's
 * owner key, which the new daemon pins so it joins only this world, and
 * the address its leaf node dials.
 */
function addHost(w: WorldInfo): HTMLElement | null {
  if (!w.ready || !w.owner) return null
  const where = leafAddress(w.leafUrl)
  const command = `skylands daemon install --world ${w.owner} --nats-url ${where.url} --host <name>`
  return section('Add a host', [
    el('div', { class: 'addhost' }, [
      el('p', { class: 'muted', style: 'margin:0;font-size:13px' }, [
        'On the machine to add, with skylands installed, run this. ',
        el('code', { text: '--world' }),
        ' names this world’s owner key, so the daemon joins this world and no other.',
      ]),
      el('pre', {}, [el('code', { text: command })]),
    ]),
    el('div', {}, [copyButton(command, 'Copy the install command', 'btn small')]),
    where.note ? hint(where.note) : null,
  ])
}

/** The owner's public key, shortened in the middle, with its whole value in a tooltip and a Copy button. */
function ownerKey(key: string): HTMLElement {
  if (!key) return el('span', { text: 'unknown' })
  return el('span', { class: 'keyval' }, [el('code', { class: 'key', title: key, text: shortId(key) }), copyButton(key, 'Copy the owner key')])
}

const loopback = ['127.0.0.1', 'localhost', '[::1]']
const anyAddress = ['0.0.0.0', '[::]']

/**
 * The leaf address another machine dials, as best this page can tell. A
 * listener on every address is reached by whatever name reached the
 * citadel, unless that is this machine's own loopback name.
 */
function leafAddress(leafUrl: string | undefined): { url: string; note: string | null } {
  if (!leafUrl) return { url: 'nats-leaf://<the world’s leaf address>', note: 'The citadel wasn’t told where daemons dial; citadel up’s --leaf-url sets it.' }
  let u: URL
  try {
    u = new URL(leafUrl.replace(/^nats-leaf:/, 'http:'))
  } catch {
    return { url: leafUrl, note: null }
  }
  if (loopback.includes(u.hostname)) {
    return { url: leafUrl, note: `The world’s leaf listener is at ${leafUrl}, which only this machine can reach. For another machine, listen on an address it can reach, such as devworld’s --leaf-listen, and use that address here.` }
  }
  if (!anyAddress.includes(u.hostname)) return { url: leafUrl, note: null }
  const here = location.hostname
  if (here && !loopback.includes(here) && here !== '::1') {
    return { url: `nats-leaf://${here.includes(':') ? `[${here}]` : here}:${u.port}`, note: `The leaf listener accepts every address; this uses ${here}, the name this page reached the citadel by. Use another if the new machine knows this one by another.` }
  }
  return { url: `nats-leaf://<this machine’s address>:${u.port}`, note: 'The leaf listener accepts every address. Use this machine’s address as the new machine sees it.' }
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
  const world: [string, string | Node][] = s.world?.ready
    ? [
        ['World', s.world.name ?? 'unnamed'],
        ['Owner key', ownerKey(s.world.owner ?? '')],
      ]
    : []
  panel.appendChild(
    factsBox([
      ...world,
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

  if (s.world) {
    const add = addHost(s.world)
    if (add) panel.appendChild(add)
  }

  panel.appendChild(
    section('NATS', [
      hint('The NATS server this citadel is connected to, as the server describes itself. Every tether ends at it.'),
      factsBox(natsFacts(s.server, s.link)),
    ]),
  )
}
