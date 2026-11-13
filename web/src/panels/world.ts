import { el } from '../dom'
import { citadelStatus, islandCounts, isStale, shortId } from '../status'
import { waitingCount, type State } from '../store'
import type { ThemeInfo } from '../protocol'
import { WORLD_THEMES, type WorldThemeId } from '../worldthemes'
import { copyButton, factsBox, header, hint, section } from './parts'

/**
 * The list of the world's themes: the built-in ones, then the theme packs
 * the world has. A theme changes how the 3D world is drawn in this
 * browser, and nothing else on the page.
 */
function themePicker(s: State, choose: (id: WorldThemeId) => void): HTMLElement {
  const packs: ThemeInfo[] = s.themes
  const offered = (id: string): boolean => WORLD_THEMES.some((t) => t.id === id) || packs.some((p) => p.id === id)
  // A pack this browser chose that the world no longer has is drawn as Default.
  const current = offered(s.worldTheme) ? s.worldTheme : 'default'
  const option = (id: string, name: string): HTMLElement => el('option', { value: id, text: name, selected: id === current ? 'true' : null })
  const select = el(
    'select',
    { class: 'theme-select', 'aria-label': 'World theme', onchange: (e: Event) => choose((e.target as HTMLSelectElement).value) },
    [
      el('optgroup', { label: 'Built in' }, WORLD_THEMES.map((t) => option(t.id, t.name))),
      packs.length ? el('optgroup', { label: 'Theme packs' }, packs.map((p) => option(p.id, p.name))) : null,
    ],
  ) as HTMLSelectElement
  select.value = current
  const chosen = packs.find((p) => p.id === current)
  const problem = s.themeProblem && s.themeProblem.id === current ? s.themeProblem.message : null
  return section('World theme', [
    select,
    problem ? hint(`${chosen?.name ?? current} couldn’t be drawn, so the world shows Default: ${problem}.`) : null,
    !problem && chosen?.description ? hint(chosen.description) : null,
    !offered(s.worldTheme) && s.link === 'connected' ? hint('The theme this browser chose isn’t in this world anymore, so it shows Default.') : null,
    hint('Find theme packs to add with skylands theme search.'),
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
  panel.appendChild(themePicker(s, setTheme))
  panel.appendChild(
    section('Settings', [
      hint('Islands, approvers, notifications, the app, and the NATS server are in Settings.'),
      el('div', {}, [el('button', { type: 'button', class: 'btn small', text: 'Open Settings', onclick: openSettings })]),
    ]),
  )
}
