import { base, fmtBytes, sortedFiles } from '../files'
import type { Island, Repo } from '../protocol'
import { backButton, factsBox, header } from './parts'

/** The file-level panel: one building. */
export function renderFilePanel(panel: HTMLElement, repo: Repo, island: Island, idx: number, back: () => void): void {
  panel.textContent = ''
  const f = sortedFiles(repo)[idx]
  if (!f) {
    back()
    return
  }
  panel.appendChild(header(base(f.path), f.path, ['Idle', 'var(--idle)']))
  panel.appendChild(backButton('repo', repo.name, back))
  panel.appendChild(
    factsBox([
      ['Terrace', `${repo.name} on ${island.host}`],
      ['Size', fmtBytes(f.size)],
      ['Agents here now', 'None'],
    ]),
  )
}
