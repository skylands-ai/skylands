import { el } from '../dom'
import type { FetchCache, Fetched } from '../fetchcache'
import type { Island, Repo, Session } from '../protocol'
import { agentLabel, clockText, sessionLabel, sessionStatus } from '../status'
import { backButton, factsBox, header, hint, section, timeNode } from './parts'

function pct(used?: number, size?: number): string {
  if (!used || !size) return 'not reported'
  return `${Math.round((used / size) * 100)}% of ${Math.round(size / 1000)}k tokens`
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

export interface AgentActions {
  back(): void
  /** Opens this session's conversation in the inbox. */
  conversation(): void
}

/** The agent-level panel: one session's facts, plan, activity, and changes. The conversation itself is in the inbox. */
export function renderAgentPanel(
  panel: HTMLElement,
  s: Session,
  repo: Repo,
  island: Island,
  stale: boolean,
  waiting: boolean,
  cache: FetchCache,
  actions: AgentActions,
): void {
  panel.textContent = ''
  const status = sessionStatus(s, waiting)
  const running = s.status === 'running'
  const back = actions.back
  panel.appendChild(header(sessionLabel(s, 60), `${agentLabel(s.agent)}${s.model ? `, ${s.model}` : ''} \u00b7 ${repo.name} on ${island.host}`, stale ? ['Last state heard', 'var(--idle)'] : status))
  panel.appendChild(backButton('repo', repo.name, back))
  panel.appendChild(el('button', { type: 'button', class: 'btn primary', style: 'align-self:flex-start', onclick: actions.conversation }, [chatIcon(), s.pending.length ? 'Go to conversation, it needs your approval' : waiting ? 'Go to conversation, it is waiting for you' : 'Go to conversation']))
  const facts: [string, string | Node][] = [
    ['Status', status[0]],
    ['Agent', s.agentName && s.agentName !== agentLabel(s.agent) ? `${agentLabel(s.agent)} (${s.agentName})` : agentLabel(s.agent)],
    ['Turns', String(s.turns)],
    ['Branch', s.branch],
    ['Started', timeNode(s.startedAt)],
    running ? ['Last activity', timeNode(s.updatedAt)] : s.status === 'idle' ? ['Idle since', timeNode(s.updatedAt)] : ['Ended', s.endedAt ? timeNode(s.endedAt) : 'unknown'],
    ['Context', pct(s.used, s.size)],
    ['Tool calls', String(s.recent.length)],
  ]
  if (s.resumes && s.resumed) {
    // The daemon under the session restarted. The history was never at risk; what came back was the agent.
    const how = s.resumed.how === 'load' ? 'the agent reloaded its own session' : 'a new agent was given the transcript'
    facts.push(['Daemon restarts', el('span', {}, [`${s.resumes}, last `, timeNode(s.resumed.restartedAt, 'ago'), `; ${how}`])])
  }
  panel.appendChild(factsBox(facts))

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

  // While the agent runs its changes grow, so keep fetching them. Afterwards each event is enough.
  const refresh = running ? 2000 : 0
  const since = Date.parse(s.updatedAt)

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

  panel.appendChild(section('Changes in the worktree', [textBlock(cache.get('diff', island.host, s.id, refresh, since), 'No changes yet.', 'diff')]))
  panel.appendChild(hint(`The worktree is on branch ${s.branch} on ${island.host}. The diff stays there until you open this panel.`))
}

function chatIcon(): HTMLElement {
  const icon = el('span', { 'aria-hidden': 'true', style: 'display:inline-flex' })
  icon.innerHTML = '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z"/></svg>'
  return icon
}
