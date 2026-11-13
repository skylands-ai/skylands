import { canSign, type ApproverActions } from './approvers'
import { el, heldKeyboard } from './dom'
import { notifySupport } from './notify'
import { approversContent, notAnApprover } from './panels/approvers'
import { copyButton, factsBox, header, hint, note, placeIcon, section, timeNode } from './panels/parts'
import type { Island, ServerInfo, WorldInfo } from './protocol'
import { pushOrigin, pushSupport } from './push'
import { islandStatus, isStale, shortId } from './status'
import { islandList, SETTINGS_PAGES, type SettingsPage, type State } from './store'
import type { Status } from './world/world'

/** What this browser can do about itself: ask to keep its storage, and install the citadel as an app. */
export interface BrowserActions {
  keep(): void
  install(): void
}

export interface SettingsActions {
  /** Opens a page of Settings. */
  page(page: SettingsPage): void
  /** Shows an island in the world. */
  openIsland(host: string): void
  /** Turns this browser's notifications on or off. */
  setNotify(on: boolean): void
  approvers: ApproverActions
  browser: BrowserActions
}

/** Each page's title, and the line under it. */
const PAGES: Record<SettingsPage, { title: string; about: string }> = {
  islands: { title: 'Islands', about: 'The hosts in this world. A row flies to its island in the world.' },
  approvers: { title: 'Approvers', about: 'Browsers that can approve what agents do in this world.' },
  nats: { title: 'NATS', about: 'The NATS server this citadel is connected to, as the server describes itself. Every tether ends at it.' },
  notifications: { title: 'Notifications', about: 'Each browser that opens the citadel has its own switch. This one is this browser’s.' },
  app: { title: 'App', about: 'Skylands as an app of its own, in browsers that make one.' },
}

/** A browser window with its tab bar, for the pages about this browser. */
function browserIcon(): HTMLElement {
  const icon = el('span', { 'aria-hidden': 'true', style: 'display:inline-flex' })
  icon.innerHTML =
    '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1.5"/><path d="M1.75 6h12.5"/><circle cx="4" cy="4.4" r=".5" fill="currentColor"/><circle cx="5.8" cy="4.4" r=".5" fill="currentColor"/></svg>'
  return icon
}

/** The pages, in two groups: the world's, which are the same in every browser, and this browser's own. */
const GROUPS: { id: string; title: string; icon: () => HTMLElement; pages: SettingsPage[] }[] = [
  { id: 'world', title: 'World', icon: () => placeIcon('world'), pages: ['islands', 'approvers', 'nats'] },
  { id: 'browser', title: 'This browser', icon: browserIcon, pages: ['notifications', 'app'] },
]

/** Whether the citadel is connected to its NATS server, as this page can tell. */
function natsStatus(s: State): Status {
  if (s.link !== 'connected') return ['Unknown', 'var(--idle)']
  if (!s.server) return ['Waiting', 'var(--warn)']
  if (!s.server.connected) return ['Connecting', 'var(--warn)']
  return ['Connected', 'var(--ok)']
}

/** Whether this browser's notifications are on, in a word. */
function notifyWord(s: State): string {
  if (notifySupport() !== 'ok') return 'Unavailable'
  if (Notification.permission === 'denied') return 'Blocked'
  return s.notify && Notification.permission === 'granted' ? 'On' : 'Off'
}

/** Whether the citadel can be installed as an app here, in a word, for a browser that approves. */
function appWord(s: State): string {
  if (!canSign(s)) return ''
  switch (s.install) {
    case 'offered':
      return 'Available'
    case 'installed':
    case 'app':
      return 'Installed'
    default:
      return ''
  }
}

/** What a page's entry in the list says beside its name: how many, or how it stands. */
function markText(s: State, page: SettingsPage): string {
  switch (page) {
    case 'islands':
      return String(Object.keys(s.islands).length)
    case 'approvers':
      return s.approvers ? String(s.approvers.filter((a) => a.valid).length) : ''
    case 'nats':
      return natsStatus(s)[0]
    case 'notifications':
      return notifyWord(s)
    case 'app':
      return appWord(s)
  }
}

function navMark(s: State, page: SettingsPage): HTMLElement | null {
  const text = markText(s, page)
  if (!text) return null
  if (page === 'islands' || page === 'approvers') return el('span', { class: 'when' }, [el('span', { class: 'settings-count', text })])
  if (page === 'nats') return el('span', { class: 'when' }, [el('span', { class: 'dot', style: `background:${natsStatus(s)[1]}` }), text])
  return el('span', { class: 'when', text })
}

/** The list of pages, by group. */
function navList(s: State, actions: SettingsActions): HTMLElement {
  return el(
    'nav',
    { class: 'settings-nav', 'aria-label': 'Settings pages' },
    GROUPS.map((g) =>
      el('section', { class: 'settings-group', 'aria-labelledby': `settings-group-${g.id}` }, [
        el('h3', { id: `settings-group-${g.id}` }, [g.icon(), g.title]),
        ...g.pages.map((page) =>
          el(
            'button',
            {
              type: 'button',
              class: 'conv-row',
              'data-keep': `settings-${page}`,
              'aria-current': page === s.settingsPage ? 'page' : 'false',
              onclick: () => actions.page(page),
            },
            [el('span', { class: 'name', text: PAGES[page].title }), navMark(s, page)],
          ),
        ),
      ]),
    ),
  )
}

/** A card on a page, with a heading when it has one. */
function card(label: string, kids: (Node | null)[], title?: string): HTMLElement {
  return el('section', { class: 'settings-card', 'aria-label': label }, [title ? el('h3', { text: title }) : null, ...kids])
}

/**
 * The islands as a table: a dot for whether the citadel reaches each one,
 * its name, its platform, its daemon's version, and when the citadel last
 * saw it. A row opens its island in the world.
 */
function islandTable(islands: Island[], stale: boolean, open: (host: string) => void): HTMLElement {
  const head = el('tr', {}, [
    el('th', { scope: 'col' }, [el('span', { class: 'sr-only', text: 'Link' })]),
    ...['Name', 'Arch', 'Version', 'Last seen'].map((h) => el('th', { scope: 'col', text: h })),
  ])
  const rows = islands.map((i) => {
    const link = i.reachable ? 'Connected' : 'Unreachable'
    const platform = `${i.daemon.os}/${i.daemon.arch}`
    // Written as the header and the island panel write it, such as v0.0.6.
    const version = i.daemon.version
    const row = el('tr', { tabindex: '0', 'data-keep': `island-${i.host}`, 'aria-label': `${i.host}, ${link}, ${platform}, ${version}`, onclick: () => open(i.host) }, [
      el('td', {}, [el('span', { class: 'dot', title: link, style: `width:10px;height:10px;background:${islandStatus(i, stale)[1]}` })]),
      el('td', { class: 'name', title: i.host, text: i.host }),
      el('td', { text: platform }),
      el('td', { class: 'version', title: version, text: version }),
      el('td', { class: 'seen' }, [timeNode(i.lastSeen)]),
    ])
    row.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        open(i.host)
      }
    })
    return row
  })
  return el('table', { class: 'islands-table' }, [el('thead', {}, [head]), el('tbody', {}, rows)])
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
  return card(
    'Add a host',
    [
      el('div', { class: 'addhost' }, [hint('On the machine to add, with skylands installed, run this.'), el('pre', {}, [el('code', { text: command })])]),
      el('div', {}, [copyButton(command, 'Copy the install command', 'btn small')]),
      where.note ? hint(where.note) : null,
    ],
    'Add a host',
  )
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

/** What the Notifications switch says it does here. */
function notifyNote(s: State): string {
  if (Notification.permission === 'denied') return 'This browser blocks notifications from this page. Allow them in its settings for this site, then turn them on here.'
  if (s.pushError) return `Push notifications couldn’t be turned on: ${s.pushError}`
  const away = 'Notifies on this device when an agent asks for approval or has a question, while this page is in the background.'
  switch (pushSupport(s)) {
    case 'ok':
      return 'Notifies on this device when an agent asks for approval or has a question, even with this page closed.'
    case 'elsewhere':
      return `${away} Open the citadel at ${pushOrigin(s)} to get them with the page closed too.`
    default:
      return away
  }
}

/**
 * The switch that turns on notifications in this browser, and what it
 * does: when an agent asks for approval or has a question. Turning it on
 * asks the browser's permission. Where the citadel sends pushes, they come
 * as pushes, which reach the browser with the page closed; otherwise only
 * while the page is open and out of sight. Browsers notify only for a page
 * with a secure context.
 */
function notifyControls(s: State, choose: (on: boolean) => void): HTMLElement[] {
  const support = notifySupport()
  if (support === 'unsupported') return [note('This browser can’t show notifications from a page.')]
  if (support === 'insecure') {
    return [note('Browsers show notifications only from a page opened over HTTPS, or at localhost or 127.0.0.1. Open the citadel at one of those to turn them on.')]
  }
  const shown = s.notify && Notification.permission === 'granted'
  return [
    el('div', { class: 'modes', role: 'group', 'aria-label': 'Notifications' }, [
      el('button', { type: 'button', class: 'btn small', 'aria-pressed': shown ? 'true' : 'false', text: 'On', onclick: () => choose(true) }),
      el('button', { type: 'button', class: 'btn small', 'aria-pressed': shown ? 'false' : 'true', text: 'Off', onclick: () => choose(false) }),
    ]),
    note(notifyNote(s)),
  ]
}

/** The Notifications switch as a section of its own, as the phone shows it under its list. */
export function notifySwitch(s: State, choose: (on: boolean) => void): HTMLElement {
  return section('Notifications', notifyControls(s, choose))
}

/**
 * The offer to install the citadel as an app, where the browser makes one,
 * word that it is installed, or why there's no offer. Only an approver gets
 * the offer: the app approves with this browser's key.
 */
function appContent(s: State, install: () => void): HTMLElement[] {
  if (!canSign(s)) return [notAnApprover(s)]
  switch (s.install) {
    case 'offered':
      return [
        el('div', { class: 'install-app' }, [
          el('button', { type: 'button', class: 'btn small', text: 'Install as an app', onclick: install }),
          hint('Skylands then opens in its own window, like the other apps on this computer, and approves with the same key as this browser.'),
        ]),
      ]
    case 'installed':
      return [hint('Skylands is installed as an app on this computer. Open it like any other app. It approves with the same key as this browser.')]
    case 'app':
      return [hint('This is the Skylands app. It approves with the same key as the browser it was installed from.')]
    default:
      return [
        note(
          'This browser isn’t offering to install Skylands as an app right now. Chrome, Edge, and other Chromium browsers offer it for a page opened over HTTPS or at localhost, unless it’s installed already.',
        ),
      ]
  }
}

function pageContent(s: State, actions: SettingsActions): (Node | null)[] {
  switch (s.settingsPage) {
    case 'islands': {
      const islands = islandList(s)
      return [
        card('Islands', [islands.length ? islandTable(islands, isStale(s), actions.openIsland) : note('No islands yet. Start a daemon on a machine and its island rises here.')]),
        s.world ? addHost(s.world) : null,
      ]
    }
    case 'approvers':
      return [card('Approvers', approversContent(s, actions.approvers, actions.browser.keep))]
    case 'nats':
      return [card('NATS server', [factsBox(natsFacts(s.server, s.link))])]
    case 'notifications':
      return [card('Notifications', notifyControls(s, actions.setNotify))]
    case 'app':
      return [card('Install as an app', appContent(s, actions.browser.install))]
  }
}

/** The page open in Settings: its title, what it's about, and its cards. */
function pageView(s: State, actions: SettingsActions): HTMLElement {
  const page = PAGES[s.settingsPage]
  return el('div', { class: 'settings-page' }, [
    el('div', { class: 'settings-body' }, [header(page.title, page.about, s.settingsPage === 'nats' ? natsStatus(s) : null), ...pageContent(s, actions)]),
  ])
}

/**
 * What Settings is drawn from: the list's marks, and what the open page
 * shows. The store replaces a record's object only when that record
 * changes, so comparing these by identity tells whether to draw again.
 */
function settingsDeps(s: State): unknown[] {
  const marks = SETTINGS_PAGES.map((p) => markText(s, p))
  switch (s.settingsPage) {
    case 'islands':
      return ['islands', ...marks, isStale(s), s.world, ...islandList(s)]
    case 'approvers':
      return ['approvers', ...marks, s.world, s.approvers, s.me, s.persisted, s.invite, s.revoking, s.approverError, s.invite ? Date.now() > Date.parse(s.invite.expires) : false]
    case 'nats':
      return ['nats', ...marks, s.server, s.link]
    case 'notifications':
      return ['notifications', ...marks, s.notify, s.push, s.pushError, s.world, notifySupport() === 'ok' ? Notification.permission : '']
    case 'app':
      return ['app', ...marks, s.install, s.world, s.approvers, s.me]
  }
}

let lastDeps: unknown[] | null = null

/**
 * Draws Settings when what it shows has changed, and leaves it alone
 * otherwise. The list and the page keep where they were scrolled, and a
 * control with the keyboard keeps it, unless another page opened, which
 * starts at its top.
 */
export function renderSettings(root: HTMLElement, s: State, actions: SettingsActions): void {
  const deps = settingsDeps(s)
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  const turned = lastDeps?.[0] !== deps[0]
  lastDeps = deps
  const navTop = root.querySelector<HTMLElement>(':scope > .settings-nav')?.scrollTop ?? 0
  const pageTop = turned ? 0 : (root.querySelector<HTMLElement>(':scope > .settings-page')?.scrollTop ?? 0)
  const keyboard = heldKeyboard(root)
  const nav = navList(s, actions)
  const page = pageView(s, actions)
  root.replaceChildren(nav, page)
  nav.scrollTop = navTop
  page.scrollTop = pageTop
  keyboard?.()
}
