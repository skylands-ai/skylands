import { el } from '../dom'
import { fmtBytes, shortCommit, sortedFiles } from '../files'
import type { Island, Repo } from '../protocol'
import { agoText } from '../status'
import { factsBox, header, hint, note, section } from './parts'

/** The terrace-level panel: one repository on one host. */
export function renderTerracePanel(
  panel: HTMLElement,
  repo: Repo,
  island: Island,
  stale: boolean,
  shown: number,
  selectFile: (idx: number) => void,
): void {
  panel.textContent = ''
  const down = stale || !island.reachable
  panel.appendChild(
    header(repo.name, `Repo on ${island.host}`, down ? ['Unreachable, last state heard', 'var(--idle)'] : ['No agents yet', 'var(--ok)']),
  )
  const facts: [string, string][] = [
    ['Path', repo.path],
    ['Commit', shortCommit(repo.commit)],
    ['Tracked files', String(repo.totalFiles)],
  ]
  if (shown < repo.totalFiles) facts.push(['Buildings shown', `${shown} largest`])
  facts.push(['Registered', agoText(repo.registeredAt)], ['Terrain snapshot', agoText(repo.snapshottedAt)])
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
  panel.appendChild(hint('Click a building to look closer.'))
}
