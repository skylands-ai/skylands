import { el } from '../dom'
import type { Island, ServerInfo, WorldInfo } from '../protocol'
import { citadelStatus, islandCounts, islandStatus, isStale, shortId } from '../status'
import { islandList, waitingCount, type State } from '../store'
import { WORLD_THEMES, type WorldThemeId } from '../worldthemes'
import { notifySupport } from '../notify'
import type { ApproverActions } from '../approvers'
import { approversSection, type BrowserActions } from './approvers'
import { copyButton, factsBox, foldSection, header, hint, note, section } from './parts'

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
  return section('World theme', [
    el(
      'div',
      { class: 'modes', role: 'group', 'aria-label': 'World theme' },
      WORLD_THEMES.map((t) =>
        el('button', { type: 'button', class: 'btn small', 'aria-pressed': t.id === current ? 'true' : 'false', text: t.name, onclick: () => choose(t.id) }),
      ),
    ),
  ])
}

/**
 * The switch that turns on notifications in this browser: when an agent
 * asks for approval or has a question while the page is out of sight.
 * Turning it on asks the browser's permission. Browsers notify only for a
 * page with a secure context.
 */
function notifySwitch(on: boolean, choose: (on: boolean) => void): HTMLElement {
  const support = notifySupport()
  if (support === 'unsupported') return section('Notifications', [note('This browser can\u2019t show notifications from a page.')])
  if (support === 'insecure') {
    return section('Notifications', [note('Browsers show notifications only from a page opened over HTTPS, or at localhost or 127.0.0.1. Open the citadel at one of those to turn them on.')])
  }
  const permission = Notification.permission
  const shown = on && permission === 'granted'
  return section('Notifications', [
    el(
      'div',
      { class: 'modes', role: 'group', 'aria-label': 'Notifications' },
      [
        el('button', { type: 'button', class: 'btn small', 'aria-pressed': shown ? 'true' : 'false', text: 'On', onclick: () => choose(true) }),
        el('button', { type: 'button', class: 'btn small', 'aria-pressed': shown ? 'false' : 'true', text: 'Off', onclick: () => choose(false) }),
      ],
    ),
    note(
      permission === 'denied'
        ? 'This browser blocks notifications from this page. Allow them in its settings for this site, then turn them on here.'
        : 'Notifies on this computer when an agent asks for approval or has a question, while this page is in the background.',
    ),
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
  return el('div', { class: 'sub-sec', 'aria-label': 'Add a host' }, [
    el('h4', { text: 'Add a host' }),
    el('div', { class: 'addhost' }, [
      el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'On the machine to add, with skylands installed, run this.' }),
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
 * citadel, unless that is this machine's own loopback name. The only note
 * is for a citadel that wasn't told the address, which the command can't
 * show.
 */
function leafAddress(leafUrl: string | undefined): { url: string; note: string | null } {
  if (!leafUrl) return { url: 'nats-leaf://<the world’s leaf address>', note: 'The citadel wasn’t told where daemons dial; citadel up’s --leaf-url sets it.' }
  let u: URL
  try {
    u = new URL(leafUrl.replace(/^nats-leaf:/, 'http:'))
  } catch {
    return { url: leafUrl, note: null }
  }
  if (!anyAddress.includes(u.hostname)) return { url: leafUrl, note: null }
  const here = location.hostname
  if (here && !loopback.includes(here) && here !== '::1') {
    return { url: `nats-leaf://${here.includes(':') ? `[${here}]` : here}:${u.port}`, note: null }
  }
  return { url: `nats-leaf://<this machine’s address>:${u.port}`, note: null }
}

/**
 * The islands as a table: a dot for whether the citadel reaches each one,
 * its name, its platform, and its daemon's version. A row opens its island.
 */
function islandTable(islands: Island[], stale: boolean, select: (id: string) => void): HTMLElement {
  const head = el('tr', {}, [el('th', { scope: 'col' }, [el('span', { class: 'sr-only', text: 'Link' })]), ...['Name', 'Arch', 'Version'].map((h) => el('th', { scope: 'col', text: h }))])
  const rows = islands.map((i) => {
    const link = i.reachable ? 'Connected' : 'Unreachable'
    const platform = `${i.daemon.os}/${i.daemon.arch}`
    // Written as the header and the island panel write it, such as v0.0.6.
    const version = i.daemon.version
    const row = el('tr', { tabindex: '0', 'aria-label': `${i.host}, ${link}, ${platform}, ${version}`, onclick: () => select(i.host) }, [
      el('td', {}, [el('span', { class: 'dot', title: link, style: `width:10px;height:10px;background:${islandStatus(i, stale)[1]}` })]),
      el('td', { class: 'name', title: i.host, text: i.host }),
      el('td', { text: platform }),
      el('td', { class: 'version', title: version, text: version }),
    ])
    row.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        select(i.host)
      }
    })
    return row
  })
  return el('table', { class: 'islands-table' }, [el('thead', {}, [head]), el('tbody', {}, rows)])
}

/** The world-level panel: the citadel itself, the world's theme, its islands, and the NATS server it is on. */
export function renderWorldPanel(panel: HTMLElement, s: State, select: (id: string) => void, setTheme: (id: WorldThemeId) => void, setNotify: (on: boolean) => void, approvers: ApproverActions, browser: BrowserActions): void {
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
      ...(s.world?.version ? [['Version', s.world.version] as [string, string]] : []),
    ]),
  )
  panel.appendChild(themeSwitch(s.worldTheme, setTheme))
  panel.appendChild(notifySwitch(s.notify, setNotify))

  const islands = islandList(s)
  panel.appendChild(
    foldSection(
      'islands',
      'Islands',
      [islands.length ? islandTable(islands, stale, select) : note('No islands yet. Start a daemon on a machine and its island rises here.'), s.world ? addHost(s.world) : null],
      islands.length,
    ),
  )

  const appr = approversSection(s, approvers, browser)
  if (appr) panel.appendChild(appr)

  panel.appendChild(
    foldSection('nats', 'NATS', [
      hint('The NATS server this citadel is connected to, as the server describes itself. Every tether ends at it.'),
      factsBox(natsFacts(s.server, s.link)),
    ]),
  )
}
