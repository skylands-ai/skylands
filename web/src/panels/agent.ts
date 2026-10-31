import { el } from '../dom'
import type { FetchCache, Fetched } from '../fetchcache'
import type { Island, Repo, Session } from '../protocol'
import { agentColor, clockText, sessionStatus } from '../status'
import { backButton, factsBox, header, hint, note, section, timeNode } from './parts'

function pct(used?: number, size?: number): string {
  if (!used || !size) return 'not reported'
  return `${Math.round((used / size) * 100)}% of ${Math.round(size / 1000)}k tokens`
}

/** The agent's own words from a transcript: what follows the prompt, before the closing line. */
function agentReply(transcript: string): string {
  let text = transcript
  const start = text.indexOf('# Agent\n\n')
  if (start >= 0) text = text.slice(start + '# Agent\n\n'.length)
  const end = text.lastIndexOf('\n\n---\n\n')
  if (end >= 0) text = text.slice(0, end)
  return text.trim()
}

function textBlock(f: Fetched, empty: string, name: string): HTMLElement {
  if (f.loading && f.at === 0) return el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: 'Fetching from the host\u2026' })
  if (f.error) return el('p', { class: 'note', style: 'color:var(--bad)', text: f.error })
  if (!f.text.trim()) return el('p', { class: 'muted', style: 'margin:0;font-size:13px', text: empty })
  const pre = el('pre', { class: 'code', 'data-scroll': name, style: 'margin:0;white-space:pre-wrap;max-height:40vh;overflow:auto;font-size:12px;line-height:1.45' })
  pre.textContent = f.text
  const kids: (Node | null)[] = [pre]
  if (f.truncated) kids.push(el('p', { class: 'muted', style: 'margin:4px 0 0;font-size:12px', text: 'Cut off at the host\u2019s size limit.' }))
  return el('div', {}, kids)
}

/** The agent-level panel: one session, with its plan, activity, response, and changes. */
export function renderAgentPanel(
  panel: HTMLElement,
  s: Session,
  repo: Repo,
  island: Island,
  stale: boolean,
  cache: FetchCache,
  back: () => void,
): void {
  panel.textContent = ''
  const status = sessionStatus(s)
  const running = s.status === 'running'
  panel.appendChild(header(s.agentName ?? s.agent, `${s.model ?? 'default model'} \u00b7 ${repo.name} on ${island.host}`, stale ? ['Last state heard', 'var(--idle)'] : status))
  panel.appendChild(backButton('repo', repo.name, back))
  panel.appendChild(note(s.prompt))
  panel.appendChild(
    factsBox([
      ['Status', status[0]],
      ['Branch', s.branch],
      ['Started', timeNode(s.startedAt)],
      [running ? 'Working for' : 'Ended', running ? timeNode(s.startedAt, 'since') : s.endedAt ? timeNode(s.endedAt) : 'unknown'],
      ['Context', pct(s.used, s.size)],
      ['Tool calls', String(s.recent.length)],
    ]),
  )

  if (s.plan?.length) {
    panel.appendChild(
      section('Plan', [
        el(
          'ul',
          { class: 'plan' },
          s.plan.map((p) =>
            el('li', { class: p.status === 'completed' ? 'done' : '' }, [
              el('span', { 'aria-hidden': 'true', text: p.status === 'completed' ? '\u2713' : p.status === 'in_progress' ? '\u25b6' : '\u25cb' }),
              el('span', { text: p.content }),
            ]),
          ),
        ),
      ]),
    )
  }

  // While the agent runs its answer grows, so keep fetching it. Afterwards once is enough.
  const refresh = running ? 2000 : 0
  const transcript = cache.get('transcript', island.host, s.id, refresh)
  const copyBtn = el('button', { type: 'button', class: 'btn small', style: 'align-self:flex-start', text: 'Copy response' })
  copyBtn.addEventListener('click', () => {
    const reply = agentReply(transcript.text)
    navigator.clipboard?.writeText(reply).then(
      () => {
        copyBtn.textContent = 'Copied'
        setTimeout(() => (copyBtn.textContent = 'Copy response'), 1500)
      },
      () => (copyBtn.textContent = 'Copy failed, select the text instead'),
    )
  })
  if (!transcript.text.trim()) copyBtn.hidden = true
  panel.appendChild(section('Response', [textBlock(transcript, running ? 'Nothing said yet.' : 'The agent said nothing.', 'response'), copyBtn]))

  if (s.recent.length) {
    panel.appendChild(
      section('Activity', [
        el(
          'ol',
          { class: 'feed' },
          [...s.recent].reverse().map((tc) =>
            el('li', { class: tc.status === 'failed' ? 'bad' : '' }, [
              el('time', { text: clockText(Date.parse(tc.at)) }),
              el('span', { text: tc.title + (tc.locations?.length ? ` \u00b7 ${tc.locations.join(', ')}` : '') + (tc.status === 'in_progress' ? ' \u2026' : tc.status === 'failed' ? ' (failed)' : '') }),
            ]),
          ),
        ),
      ]),
    )
  }

  panel.appendChild(section('Changes in the worktree', [textBlock(cache.get('diff', island.host, s.id, refresh), 'No changes yet.', 'diff')]))
  panel.appendChild(hint(`The worktree is on branch ${s.branch} on ${island.host}. Transcripts and diffs stay there until you open this panel.`))
  void agentColor
}
