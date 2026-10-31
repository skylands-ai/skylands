import { el } from '../dom'
import { fmtBytes, shortCommit, sortedFiles } from '../files'
import type { Island, Repo, Session } from '../protocol'
import { agentColor, sessionStatus } from '../status'
import { backButton, factsBox, header, hint, note, section, timeNode } from './parts'

/** The terrace-level panel: one repository on one host. */
export function renderTerracePanel(
  panel: HTMLElement,
  repo: Repo,
  island: Island,
  stale: boolean,
  shown: number,
  sessions: Session[],
  selectFile: (idx: number) => void,
  selectAgent: (id: string) => void,
  backToIsland: () => void,
): void {
  panel.textContent = ''
  const down = stale || !island.reachable
  const running = sessions.filter((s) => s.status === 'running').length
  panel.appendChild(
    header(
      repo.name,
      `Repo on ${island.host}`,
      down
        ? ['Unreachable, last state heard', 'var(--idle)']
        : running
          ? [`${running} ${running === 1 ? 'agent' : 'agents'}`, 'var(--ok)']
          : [sessions.length ? 'No agents running' : 'No agents yet', 'var(--idle)'],
    ),
  )
  panel.appendChild(backButton('island', island.host, backToIsland))
  if (sessions.length) {
    panel.appendChild(
      section(
        'Agents',
        sessions.map((s) => {
          const st = sessionStatus(s)
          const file = s.status === 'running' && s.currentFile ? ` \u00b7 ${s.currentFile}` : ''
          return el('button', { type: 'button', class: 'row', style: 'text-align:left;width:100%', onclick: () => selectAgent(s.id) }, [
            el('span', { class: 'dot', style: `width:10px;height:10px;background:${agentColor(s.agent)}` }),
            el('span', { class: 'grow' }, [
              el('span', { style: 'font-weight:600' }, [
                s.agentName ?? s.agent,
                s.model ? el('span', { class: 'muted', style: 'font-weight:400', text: ` (${s.model})` }) : null,
                ' ',
                el('span', { class: 'muted', style: 'font-weight:400', text: s.prompt.length > 60 ? s.prompt.slice(0, 57) + '\u2026' : s.prompt }),
              ]),
              el('span', { class: 'sub', text: st[0] + file }),
            ]),
            el('span', { class: 'dot', title: st[0], style: `background:${st[1]}` }),
          ])
        }),
      ),
    )
  }
  const facts: [string, string | Node][] = [
    ['Path', repo.path],
    ['Commit', shortCommit(repo.commit)],
    ['Tracked files', String(repo.totalFiles)],
  ]
  if (shown < repo.totalFiles) facts.push(['Buildings shown', `${shown} largest`])
  facts.push(['Registered', timeNode(repo.registeredAt)], ['Terrain snapshot', timeNode(repo.snapshottedAt)])
  panel.appendChild(factsBox(facts))

  const files = sortedFiles(repo)
  if (files.length) {
    panel.appendChild(
      section(
        'Largest files',
        files.slice(0, 8).map((f, idx) => {
          const clickable = idx < shown
          return el(
            clickable ? 'button' : 'div',
            { type: clickable ? 'button' : null, class: 'row', style: 'text-align:left;width:100%', onclick: clickable ? () => selectFile(idx) : null },
            [
              el('span', { class: 'grow' }, [el('code', { text: f.path }), el('span', { class: 'sub', text: fmtBytes(f.size) })]),
            ],
          )
        }),
      ),
    )
  } else {
    panel.appendChild(note('This repository has no tracked files yet.'))
  }
  panel.appendChild(hint(sessions.length ? 'Click an agent to read its response, or a building to look closer.' : 'Click a building to look closer.'))
}
