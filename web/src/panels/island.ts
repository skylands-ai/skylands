import { el } from '../dom'
import type { AgentAvailability, Island, Repo, Session } from '../protocol'
import { agentLabel, clockText, islandStatus, isOpen } from '../status'
import { agentGlyph, factsBox, header, note, section, timeNode } from './parts'

/**
 * The agents an island can run, as its daemon last checked: what each is,
 * who it's logged in as, and any warning with what to do about it. An
 * agent the host can't run isn't listed; skylands preflight on the host
 * says why.
 */
function agentsSection(i: Island, current: boolean): HTMLElement {
  if (!i.agents) {
    return section('Agents', [
      el('p', {
        class: 'note',
        text: 'The daemon hasn’t said yet which agents it can run. It checks when it starts, and skylands preflight on the host asks it to check now.',
      }),
    ])
  }
  const ready = i.agents.filter((a) => a.available)
  if (!ready.length) {
    return section('Agents', [el('p', { class: 'note', text: 'This host can’t run any agent yet. On it, skylands preflight says why.' })])
  }
  return section('Agents', [current ? null : el('p', { class: 'note', text: 'As the daemon last recorded them.' }), ...ready.map(agentRow)])
}

function agentRow(a: AgentAvailability): HTMLElement {
  const what = [a.login, a.agent].filter(Boolean).join(' · ')
  // An agent that runs can still carry a warning, such as a login the
  // agent didn't report in time.
  const warnings = (a.notes ?? []).map((n) =>
    el('span', { class: 'sub note-line warn' }, [`${n.check}: ${n.found}`, n.fix ? el('span', { class: 'fix', text: `. ${n.fix[0].toUpperCase()}${n.fix.slice(1)}` }) : null]),
  )
  return el('div', { class: 'row avail' }, [
    agentGlyph(a.name),
    el('span', { class: 'grow' }, [
      el('span', { style: 'font-weight:600', text: agentLabel(a.name) }),
      what ? el('span', { class: 'sub', text: what }) : null,
      ...warnings,
    ]),
  ])
}

/** The island-level panel: one host, as the citadel last saw it. */
export function renderIslandPanel(
  panel: HTMLElement,
  i: Island,
  repos: Repo[],
  stale: boolean,
  lastContact: number | null,
  sessions: Session[],
  land: (terrace: string) => void,
): void {
  panel.textContent = ''
  const d = i.daemon
  panel.appendChild(header(i.host, `${d.os}/${d.arch}`, islandStatus(i, stale)))
  if (stale) {
    panel.appendChild(
      note(
        `This page can’t reach the citadel, so nothing here is current. It shows the island as the citadel last reported it${lastContact ? `, at ${clockText(lastContact)}` : ''}.`,
      ),
    )
    panel.appendChild(
      factsBox([
        ['Link, as last seen', i.reachable ? 'Connected' : 'Unreachable'],
        ['Daemon, as last reported', d.version],
        ['Last seen by the citadel', timeNode(i.lastSeen)],
      ]),
    )
  } else if (i.reachable) {
    const daemon = el('span', {}, [`${d.version}, `, timeNode(d.started, 'up')])
    panel.appendChild(
      factsBox([
        ['Link', 'Connected'],
        ['Daemon', daemon],
        ['First seen', timeNode(i.firstSeen)],
        ['Last seen', timeNode(i.lastSeen)],
      ]),
    )
  } else {
    panel.appendChild(
      note(
        'The citadel can’t reach this host, and it can’t tell whether the network dropped or the daemon stopped. Everything here is the last state it saw. If the daemon is still running, it’s holding its events, and any decision it needs, until the link comes back.',
      ),
    )
    panel.appendChild(
      factsBox([
        ['Last seen', timeNode(i.lastSeen)],
        ['Daemon, as last reported', d.version],
        ['First seen', timeNode(i.firstSeen)],
      ]),
    )
  }
  panel.appendChild(agentsSection(i, !stale && i.reachable))
  if (repos.length) {
    panel.appendChild(
      section(
        'Terraces',
        repos.map((r) => {
          const n = r.totalFiles
          const agents = sessions.filter((s) => s.repoId === r.id && isOpen(s)).length
          const detail = `${n} tracked ${n === 1 ? 'file' : 'files'}, ${agents ? `${agents} ${agents === 1 ? 'agent' : 'agents'} working` : 'no agents'}`
          return el('div', { class: 'row' }, [
            el('span', { class: 'grow' }, [
              el('span', { style: 'font-weight:600', text: r.name }),
              el('span', { class: 'sub', text: (!stale && i.reachable ? '' : 'As last seen: ') + detail }),
            ]),
            el('button', { type: 'button', class: 'btn small', text: 'Fly in', onclick: () => land(r.id) }),
          ])
        }),
      ),
    )
  } else {
    panel.appendChild(
      section('Terraces', [
        el('p', { class: 'note', text: `No repositories registered on this host yet. On it, run: skylands repo register <path>` }),
      ]),
    )
  }
}
