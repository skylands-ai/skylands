import { el } from '../dom'
import { citadelStatus, islandCounts, isStale, shortId } from '../status'
import { waitingCount, type State } from '../store'
import { WORLD_THEMES, type WorldThemeId } from '../worldthemes'
import { copyButton, factsBox, header, hint, section } from './parts'

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

/** The owner's public key, shortened in the middle, with its whole value in a tooltip and a Copy button. */
function ownerKey(key: string): HTMLElement {
  if (!key) return el('span', { text: 'unknown' })
  return el('span', { class: 'keyval' }, [el('code', { class: 'key', title: key, text: shortId(key) }), copyButton(key, 'Copy the owner key')])
}

/**
 * The world-level panel: the citadel itself and the world's theme, which
 * changes the view beside it. The islands, the approvers, this browser's
 * notifications and app, and the NATS server are in Settings.
 */
export function renderWorldPanel(panel: HTMLElement, s: State, setTheme: (id: WorldThemeId) => void, openSettings: () => void): void {
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
  panel.appendChild(
    section('Settings', [
      hint('Islands, approvers, notifications, the app, and the NATS server are in Settings.'),
      el('div', {}, [el('button', { type: 'button', class: 'btn small', text: 'Open Settings', onclick: openSettings })]),
    ]),
  )
}
