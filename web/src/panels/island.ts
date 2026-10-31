import { el } from '../dom'
import type { Island } from '../protocol'
import { agoText, clockText, islandStatus, upText } from '../status'
import { factsBox, header, note, section } from './parts'

/** The island-level panel: one host, as the citadel last heard it. */
export function renderIslandPanel(panel: HTMLElement, i: Island, stale: boolean, lastContact: number | null): void {
  panel.textContent = ''
  const d = i.daemon
  panel.appendChild(header(i.host, `${d.os}/${d.arch}`, islandStatus(i, stale)))
  if (stale) {
    panel.appendChild(
      note(
        `This page can\u2019t reach the citadel, so nothing here is current. It shows the island as the citadel last reported it${lastContact ? `, at ${clockText(lastContact)}` : ''}.`,
      ),
    )
    panel.appendChild(
      factsBox([
        ['Link, as last heard', i.reachable ? 'Connected' : 'Unreachable'],
        ['Daemon, as last reported', `skylands ${d.version}`],
        ['Last heard by the citadel', agoText(i.lastSeen)],
      ]),
    )
  } else if (i.reachable) {
    panel.appendChild(
      factsBox([
        ['Link', 'Connected'],
        ['Daemon', `skylands ${d.version}, ${upText(d.started)}`],
        ['First seen', agoText(i.firstSeen)],
        ['Last heard', agoText(i.lastSeen)],
      ]),
    )
  } else {
    panel.appendChild(
      note(
        'The citadel can’t reach this host, and it can’t tell whether the network dropped or the daemon stopped. Everything here is the last state it heard. If the daemon is still running, it’s holding its events, and any decision it needs, until the link comes back.',
      ),
    )
    panel.appendChild(
      factsBox([
        ['Last heard', agoText(i.lastSeen)],
        ['Daemon, as last reported', `skylands ${d.version}`],
        ['First seen', agoText(i.firstSeen)],
      ]),
    )
  }
  panel.appendChild(section('Terraces', [el('p', { class: 'note', text: 'No repositories registered on this host yet.' })]))
}
