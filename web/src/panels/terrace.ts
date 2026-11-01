import { el } from '../dom'
import { shortCommit } from '../files'
import type { Island, Repo, Rule, Session } from '../protocol'
import { HOST_DEFAULTS, rulesSheet } from '../rules'
import { agentLabel, isOpen } from '../status'
import { agentGlyph, backButton, factsBox, header, hint, note, section, timeNode } from './parts'

/** The terrace-level panel: one repository on one host. */
export function renderTerracePanel(
  panel: HTMLElement,
  repo: Repo,
  island: Island,
  stale: boolean,
  shown: number,
  sessions: Session[],
  waiting: (s: Session) => boolean,
  backToIsland: () => void,
  setRules: (rules: Rule[]) => Promise<string>,
): void {
  panel.textContent = ''
  const down = stale || !island.reachable
  const running = sessions.filter(isOpen).length
  panel.appendChild(
    header(
      repo.name,
      `Repo on ${island.host}`,
      down
        ? ['Unreachable, last state heard', 'var(--idle)']
        : running
          ? [`${running} ${running === 1 ? 'agent' : 'agents'}`, 'var(--ok)']
          : [sessions.length ? 'No agents running' : 'No agents yet', 'var(--idle)'],
      'repo',
    ),
  )
  panel.appendChild(backButton('island', island.host, backToIsland))
  if (sessions.length) panel.appendChild(section('Agents', agentRows(sessions, waiting)))
  const facts: [string, string | Node][] = [
    ['Path', repo.path],
    ['Commit', shortCommit(repo.commit)],
    ['Tracked files', String(repo.totalFiles)],
  ]
  if (shown < repo.totalFiles) facts.push(['Buildings shown', `${shown} largest`])
  facts.push(['Registered', timeNode(repo.registeredAt)], ['Terrain snapshot', timeNode(repo.snapshottedAt)])
  panel.appendChild(factsBox(facts))
  if (!repo.files.length) panel.appendChild(note('This repository has no tracked files yet.'))
  panel.appendChild(
    section('Rules', [
      el('p', { class: 'muted', style: 'margin:0 0 6px;font-size:12px', text: 'New sessions on this repository start with these rules. A session can change its own copy.' }),
      rulesSheet(repo.rules ?? [], { defaults: HOST_DEFAULTS, readOnly: down, onChange: setRules }),
    ]),
  )
  panel.appendChild(hint(sessions.length ? 'Click an airship to follow its agent, or a building to look closer. Conversations are in the inbox.' : 'Click a building to look closer.'))
}

/** One line per agent type on this terrace: its glyph, its name, and how many sessions it has open. */
function agentRows(sessions: Session[], waiting: (s: Session) => boolean): HTMLElement[] {
  const byAgent = new Map<string, Session[]>()
  for (const s of sessions) byAgent.set(s.agent, [...(byAgent.get(s.agent) ?? []), s])
  return [...byAgent.entries()]
    .sort((a, b) => b[1].filter(isOpen).length - a[1].filter(isOpen).length || a[0].localeCompare(b[0]))
    .map(([agent, list]) => {
      const open = list.filter(isOpen).length
      const onYou = list.filter(waiting).length
      const past = list.length - open
      const parts = [open ? `${open} open ${open === 1 ? 'session' : 'sessions'}` : 'No open sessions']
      if (onYou) parts.push(`${onYou} waiting for you`)
      if (past) parts.push(`${past} finished`)
      return el('div', { class: 'row' }, [
        agentGlyph(agent),
        el('span', { class: 'grow' }, [el('span', { style: 'font-weight:600', text: agentLabel(agent) }), el('span', { class: 'sub', text: parts.join(' \u00b7 ') })]),
        open ? el('span', { class: 'badge', text: String(open), 'aria-hidden': 'true' }) : null,
      ])
    })
}
