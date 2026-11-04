import { el } from '../dom'
import type { TokenUsage } from '../protocol'
import { agentColor, tokensBreakdown, tokensText } from '../status'
import { providerOf } from '../world/airship'
import { agoText, durationText, secondsSince, upText } from '../status'
import type { Status } from '../world/world'

type TimeMode = 'ago' | 'since' | 'up'

function timeText(iso: string, mode: TimeMode): string {
  if (mode === 'ago') return agoText(iso)
  if (mode === 'up') return upText(iso)
  return durationText(secondsSince(iso))
}

/** A time that keeps ticking without rebuilding the panel around it. */
export function timeNode(iso: string, mode: TimeMode = 'ago'): HTMLElement {
  return el('span', { 'data-time': iso, 'data-mode': mode, text: timeText(iso, mode) })
}

/** Updates every time node under root to now. */
export function refreshTimes(root: ParentNode): void {
  for (const n of root.querySelectorAll<HTMLElement>('[data-time]')) {
    const text = timeText(n.dataset.time as string, (n.dataset.mode as TimeMode) ?? 'ago')
    if (n.textContent !== text) n.textContent = text
  }
}

/** A panel heading with an optional status pill and a one-line description, and the glyph of the place it names when given one. */
export function header(title: string, kind: string | null, status: Status | null, place?: Place): HTMLElement {
  return el('div', { class: 'ph' }, [
    el('div', { style: 'flex:1;min-width:0' }, [
      el('div', { style: 'display:flex;flex-wrap:wrap;align-items:center;gap:10px' }, [
        el('h2', {}, [place ? placeIcon(place) : null, title]),
        status ? el('span', { class: 'pill' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]) : null,
      ]),
      kind ? el('p', { class: 'muted', style: 'margin:4px 0 0;font-size:13px', text: kind }) : null,
    ]),
  ])
}

/** A token total with the exact counts as its tooltip, or the given text while nothing has been counted. */
export function tokensNode(t: TokenUsage, none = 'none yet'): HTMLElement {
  if (!t.total) return el('span', { text: none })
  return el('span', { title: tokensBreakdown(t), text: tokensText(t.total) })
}

/** Label and value rows. A value can be a node, such as a ticking time. */
export function factsBox(rows: [string, string | Node][]): HTMLElement {
  return el(
    'div',
    { class: 'facts' },
    rows.map(([k, v]) => el('div', { class: 'fact' }, [el('span', { class: 'muted', text: k }), typeof v === 'string' ? el('b', { text: v }) : el('b', {}, [v])])),
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

/** A level of the world, so a button or crumb can carry its glyph. */
export type Place = 'world' | 'island' | 'repo'

const ICONS: Record<Place, string> = {
  // The world: a globe.
  world: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="8" cy="8" r="6.3"/><path d="M1.7 8h12.6M8 1.7c-2.2 2-2.2 10.6 0 12.6M8 1.7c2.2 2 2.2 10.6 0 12.6M3 4.4c3 1.4 7 1.4 10 0M3 11.6c3-1.4 7-1.4 10 0"/></svg>',
  // A repository: the version-control branch, a trunk from a point at the
  // bottom to a point at the top, with a branch leaving upward to an offset point.
  repo: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="5.5" cy="13" r="1.8"/><circle cx="5.5" cy="3" r="1.8"/><circle cx="12" cy="6" r="1.8"/><path d="M5.5 4.8v6.4M5.5 10.2Q5.5 7.9 12 7.8"/></svg>',
  // An island: a flat top floating on a cone of rock.
  island: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="currentColor"><path d="M2 6.2C2 4.9 4.7 4 8 4s6 .9 6 2.2S11.3 8.4 8 8.4 2 7.5 2 6.2z"/><path d="M3.6 8.3c1.2.5 2.7.8 4.4.8s3.2-.3 4.4-.8L8 14.5z" opacity=".7"/></svg>',
}

/**
 * An agent type's glyph: its airship's hull in its color, so the panel and
 * the sky agree on who is who. A long envelope for Claude, a boxy hull for
 * Codex, a round one for Gemini, a goose for goose, and a four-sided one for
 * local agents.
 */
export function agentGlyph(agent: string): HTMLElement {
  const color = agentColor(agent)
  const shapes: Record<string, string> = {
    claude: `<ellipse cx="11" cy="10" rx="9" ry="4.5" fill="${color}"/><rect x="8" y="14" width="6" height="3" rx="1" fill="${color}" opacity=".7"/>`,
    codex: `<rect x="3" y="5" width="16" height="9" rx="2.5" fill="${color}"/><rect x="8" y="14" width="6" height="3" rx="1" fill="${color}" opacity=".7"/>`,
    gemini: `<circle cx="11" cy="9" r="7" fill="${color}"/><rect x="8" y="16" width="6" height="3" rx="1" fill="${color}" opacity=".7"/>`,
    goose: `<ellipse cx="9" cy="11" rx="7" ry="4" fill="${color}"/><path d="M14 10 L17 5" stroke="${color}" stroke-width="2.2" stroke-linecap="round"/><circle cx="17.5" cy="4.5" r="2.2" fill="${color}"/><path d="M19.5 4 L22 4.6 L19.5 5.4 Z" fill="#F0A030"/><rect x="6" y="15" width="6" height="3" rx="1" fill="${color}" opacity=".7"/>`,
    local: `<path d="M11 2l8 7-8 7-8-7z" fill="${color}"/><rect x="8" y="16" width="6" height="3" rx="1" fill="${color}" opacity=".7"/>`,
  }
  const icon = el('span', { 'aria-hidden': 'true', style: 'display:inline-flex;flex:none' })
  icon.innerHTML = `<svg viewBox="0 0 22 22" width="22" height="22">${shapes[providerOf(agent)] ?? shapes.local}</svg>`
  return icon
}

/** The glyph for a level of the world, drawn in the surrounding text color. */
export function placeIcon(place: Place): HTMLElement {
  const icon = el('span', { 'aria-hidden': 'true', style: 'display:inline-flex' })
  icon.innerHTML = ICONS[place]
  return icon
}

/** A button that goes back up to a repo or an island, with a glyph for which. */
export function backButton(place: Place, name: string, onClick: () => void): HTMLElement {
  return el('button', { type: 'button', class: 'btn ghost', style: 'align-self:flex-start', onclick: onClick }, [placeIcon(place), `Back to ${name}`])
}
