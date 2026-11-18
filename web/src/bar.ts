import { compose, Draft, type ComposeParts } from './composer'
import { askingText, becauseText, choseText, joined, leftOutText, pick, place as placeWork, targetText, undecidedText, type Candidate, type Placement } from './dispatch'
import { newSessionId } from './approvers'
import { byId, el } from './dom'
import type { Choosing } from './newsession'
import { imagesRefused, startSession } from './start'
import { StartChips } from './startchips'
import { isStale } from './status'
import { level, type State } from './store'

/** What the bar needs from the page around it. */
export interface BarActions {
  /** Opens the target dialog, for the person to choose where work goes. */
  choose: (c: Choosing) => void
  /** Flies to a terrace. */
  land: (host: string, repo: string) => void
}

/**
 * Where the bar sends work: a terrace's repository, an island, whose
 * daemon picks the repository, or the world, where the citadel picks the
 * island too. An island with one repository is that repository's terrace,
 * marked only, since there's nothing to choose between.
 */
type Scope = { kind: 'terrace'; host: string; repo: string; only?: boolean } | { kind: 'island'; host: string } | { kind: 'world' }

/** Where work asked for at an island or the world would go, shown until the work is started or edited. */
interface Preview {
  prompt: string
  /** The session to start, which an agent's choice may have been asked for. */
  session: string
  target: Candidate
}

/**
 * The command bar at the bottom of the world. On a terrace or one of its
 * files it starts work in that repository. On an island, Enter shows
 * which of its repositories the work would go to, as the island's daemon
 * finds it, and on the world which island too; a second Enter starts it
 * there. When nothing settles it, the target dialog asks the person.
 */
export class CommandBar {
  private readonly root = byId<HTMLElement>('cmdbar')
  private readonly input = byId<HTMLInputElement>('cmdInput')
  private readonly go = byId<HTMLButtonElement>('cmdGo')
  /** The agent, mode, model, and Where chips, as the island last said what it can run. */
  private readonly chips = new StartChips({
    agent: byId<HTMLSelectElement>('agentSel'),
    modeChip: byId<HTMLElement>('modeChip'),
    mode: byId<HTMLSelectElement>('modeSel'),
    model: byId<HTMLSelectElement>('modelSel'),
    otherModel: byId<HTMLInputElement>('modelOther'),
    where: byId<HTMLSelectElement>('whereSel'),
  })
  private readonly scopeTo = byId<HTMLElement>('scopeTo')
  private readonly why = byId<HTMLElement>('scopeWhy')
  private scope: Scope | null = null
  private preview: Preview | null = null
  /** The state last shown, which a start is signed against. */
  private state: State | null = null
  private busy = false
  /** The files the order mentions and the images attached to it. */
  private readonly draft = new Draft()
  private readonly compose: ComposeParts
  /** The host images attached go to. */
  private sentTo = ''
  /** What the status line is showing: nothing, a link problem, an error, or a result. */
  private kind: 'none' | 'blocked' | 'error' | 'info' = 'none'

  constructor(private readonly actions: BarActions) {
    // The @ list takes the keys it uses before the bar does, so picking a
    // file doesn't launch the order. Mentions and images need a
    // repository, so they're offered on a terrace.
    this.compose = compose(this.input, this.draft, {
      files: () => (this.scope?.kind === 'terrace' ? `/api/repos/${encodeURIComponent(this.scope.host)}/${encodeURIComponent(this.scope.repo)}/files` : null),
      host: () => (this.scope?.kind === 'terrace' ? this.scope.host : null),
      noImages: () => {
        if (this.scope?.kind !== 'terrace') return 'Images go with work started on a terrace.'
        return this.state ? imagesRefused(this.state.islands[this.scope.host], this.chips.chosen().agent) : 'The world isn’t loaded yet.'
      },
    })
    const anchor = byId<HTMLElement>('cmdAnchor')
    anchor.prepend(this.compose.menu)
    anchor.appendChild(this.compose.attach)
    const tray = byId<HTMLElement>('cmdTray')
    tray.appendChild(this.compose.tray)
    const showTray = (): void => {
      tray.hidden = this.compose.tray.hidden
    }
    new MutationObserver(showTray).observe(this.compose.tray, { attributes: true, attributeFilter: ['hidden'] })
    showTray()
    byId<HTMLElement>('agentSel').addEventListener('change', () => this.compose.refresh())
    this.go.addEventListener('click', () => void this.submit())
    this.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void this.submit()
      }
    })
    // Editing the work clears where it would go, and the last error or result.
    this.input.addEventListener('input', () => {
      if (this.preview && this.preview.prompt !== this.input.value.trim()) this.clearPreview()
      if (this.kind === 'error' || this.kind === 'info') this.setWhy('', 'none')
    })
  }

  /** The bar's scope at this place in the world, or null where it doesn't show. */
  private static scopeOf(s: State): Scope | null {
    const l = level(s)
    const island = s.selected !== 'cit' ? s.islands[s.selected] : undefined
    const repo = s.terrace ? s.repos[s.terrace] : undefined
    if ((l === 'terrace' || l === 'file') && repo && island) return { kind: 'terrace', host: island.host, repo: repo.id }
    if (l === 'island' && island) {
      const repos = Object.values(s.repos).filter((r) => r.host === island.host)
      if (repos.length === 1) return { kind: 'terrace', host: island.host, repo: repos[0].id, only: true }
      return { kind: 'island', host: island.host }
    }
    if (l === 'world') return { kind: 'world' }
    return null
  }

  private static same(a: Scope | null, b: Scope | null): boolean {
    return JSON.stringify(a) === JSON.stringify(b)
  }

  /** Shows the bar on the world, an island, a terrace, or one of its files, aimed there. */
  update(s: State): void {
    this.state = s
    const scope = CommandBar.scopeOf(s)
    this.root.hidden = !scope
    if (!scope) return
    if (!CommandBar.same(scope, this.scope)) {
      this.scope = scope
      this.clearPreview()
      this.input.placeholder = scope.kind === 'terrace' ? 'Describe work for this repo; @ mentions a file' : 'Describe the work'
      // A result belongs to the place it was asked at; an error stays
      // until the work is edited.
      if (this.kind !== 'error') this.setWhy('', 'none')
    }
    // Images attached go to the island the work goes to, which may have
    // changed since they were attached.
    const host = scope.kind === 'world' ? '' : scope.host
    if (host && host !== this.sentTo) {
      this.sentTo = host
      this.draft.sendTo(host)
    }
    this.showScope(s)
    const blocked = this.blocked(s)
    this.go.disabled = !!blocked || this.busy
    this.input.disabled = !!blocked
    // A link problem shows while it lasts and clears when the link is back.
    // An error stays until the work is edited.
    if (blocked) this.setWhy(blocked, 'blocked')
    else if (this.kind === 'blocked') this.setWhy('', 'none')
  }

  /** Shows the scope, the chips for the island the work goes to, and what Enter does. */
  private showScope(s: State): void {
    const scope = this.scope
    if (!scope) return
    const target = this.preview?.target
    const islandHost = target?.host ?? (scope.kind === 'world' ? null : scope.host)
    const label =
      scope.kind === 'terrace'
        ? `${s.repos[scope.repo]?.name ?? scope.repo} on ${scope.host}`
        : target
          ? targetText(target)
          : scope.kind === 'island'
            ? `${scope.host}, any repository`
            : 'The world'
    if (this.scopeTo.textContent !== label) this.scopeTo.textContent = label
    this.chips.show(islandHost ? (s.islands[islandHost] ?? null) : null)
    this.compose.refresh()
    const text = scope.kind === 'terrace' ? 'Launch' : this.preview ? 'Start' : 'Preview'
    if (this.go.textContent !== text) this.go.textContent = text
  }

  /** Why nothing can be sent from the bar right now, or empty. */
  private blocked(s: State): string {
    const scope = this.scope
    if (!scope) return ''
    if (isStale(s)) return 'The citadel is unreachable, so nothing can be dispatched.'
    if (scope.kind === 'world') {
      const repos = Object.values(s.repos)
      if (!repos.length) return 'No repository is registered yet. Add one from an island’s panel.'
      if (!repos.some((r) => s.islands[r.host]?.reachable)) return 'No island with a repository is reachable, so nothing can be placed yet.'
      return ''
    }
    const island = s.islands[scope.host]
    if (!island?.reachable) return `${scope.host} is unreachable, so the work can’t be delivered yet.`
    if (scope.kind === 'island' && !Object.values(s.repos).some((r) => r.host === scope.host)) return `No repository is registered on ${scope.host} yet. Add one from its panel.`
    if (!this.chips.hasAgents()) return `${scope.host} can’t run any agent yet. On it, skylands preflight says why.`
    return ''
  }

  private setWhy(text: string, kind: 'none' | 'blocked' | 'error' | 'info', ...more: Node[]): void {
    if (this.why.textContent !== text || more.length || this.why.childElementCount) this.why.replaceChildren(text, ...more)
    this.why.style.color = kind === 'blocked' || kind === 'error' ? 'var(--bad)' : ''
    this.kind = kind
  }

  private clearPreview(): void {
    if (!this.preview) return
    this.preview = null
    if (this.state) this.showScope(this.state)
  }

  private async submit(): Promise<void> {
    const prompt = this.input.value.trim()
    const s = this.state
    if (!prompt || this.busy || !this.scope || !s || this.go.disabled) return
    this.busy = true
    this.go.disabled = true
    try {
      const scope = this.scope
      if (scope.kind === 'terrace') await this.start(prompt, scope.host, scope.repo, newSessionId())
      else if (this.preview && this.preview.prompt === prompt) await this.start(prompt, this.preview.target.host, this.preview.target.repoId, this.preview.session)
      else await this.place(prompt, scope)
    } finally {
      this.busy = false
      if (this.state) this.update(this.state)
    }
  }

  /** Finds where work asked for at an island or the world goes, and shows it, or hands it to the person. */
  private async place(prompt: string, scope: Scope & { kind: 'island' | 'world' }): Promise<void> {
    this.setWhy('Finding where this goes…', 'info')
    // An answer to work since edited, or asked at a place since left,
    // is dropped.
    const current = (): boolean => this.input.value.trim() === prompt && CommandBar.same(this.scope, scope)
    const res = await placeWork(prompt, scope.kind === 'island' ? scope.host : null)
    if (!current()) return
    if ('error' in res) {
      this.setWhy(res.error, 'error')
      return
    }
    const session = newSessionId()
    const where = scope.kind === 'island' ? scope.host : res.namedIsland ? res.namedIsland : 'any island'
    const away = leftOutText(res)
    if (res.do === 'start' && res.target) {
      this.showPreview({ prompt, session, target: res.target }, becauseText(res.target, res.why), away)
      return
    }
    if (res.do === 'agent' && res.host && res.among?.length) {
      this.setWhy(askingText(res.among), 'info')
      const picked = await pick(prompt, res.host, res.among.map((c) => c.repoId), this.chips.chosen().agent, session)
      if (!current()) return
      const chosen = 'error' in picked ? undefined : res.among.find((c) => c.repoId === picked.repoId)
      if (chosen && !('error' in picked)) {
        this.showPreview({ prompt, session, target: chosen }, choseText(picked.agent, chosen, res.among), away)
        return
      }
      this.handOver(prompt, session, res, 'error' in picked ? `Couldn’t choose between ${joined(res.among.map((c) => c.name))}: ${picked.error}` : undecidedText({ ...res, why: 'close' }, where))
      return
    }
    this.handOver(prompt, session, res, `${undecidedText(res, where)} ${away}`.trim())
  }

  /** Shows where the work would go, in the scope, and why, for the next Enter to start it there. */
  private showPreview(p: Preview, because: string, away: string): void {
    this.preview = p
    if (this.state) this.showScope(this.state)
    this.setWhy(`${because}.${away ? ` ${away}` : ''}`, 'info')
  }

  /** Hands the work to the person, in the target dialog, with the repositories that fit best first. */
  private handOver(prompt: string, session: string, res: Placement, note: string): void {
    const among = res.among?.length ? res.among : res.ranked.slice(0, 3)
    this.setWhy('', 'none')
    this.actions.choose({
      prompt,
      among,
      note,
      session,
      started: (id, host, repo) => this.started(id, host, repo),
    })
  }

  /** Starts the work on host's repository, as the person confirmed. */
  private async start(prompt: string, host: string, repo: string, session: string): Promise<void> {
    const s = this.state
    if (!s) return
    this.setWhy(this.draft.images.length ? 'Sending the images…' : 'Dispatching…', 'info')
    const carried = await this.draft.carried(prompt, host)
    if (typeof carried === 'string') {
      this.setWhy(carried, 'error')
      return
    }
    if (carried.images.length) this.setWhy('Dispatching…', 'info')
    const out = await startSession(s, { host, repo, ...this.chips.chosen(), prompt, ...carried }, session)
    if (out.error !== undefined) {
      this.setWhy(out.error, 'error')
      return
    }
    this.started(out.session, host, repo)
  }

  /** Says the work started, and where, with a way there from elsewhere. */
  private started(_session: string, host: string, repo: string): void {
    this.input.value = ''
    this.draft.clear()
    this.clearPreview()
    if (this.scope?.kind === 'terrace' && !this.scope.only) {
      this.setWhy('Dispatched. The agent appears on the terrace as it starts.', 'info')
      return
    }
    const name = this.state?.repos[repo]?.name ?? repo
    this.setWhy(`Started on ${name} at ${host}. `, 'info', el('button', { type: 'button', class: 'linkish', text: 'Show it', onclick: () => this.actions.land(host, repo) }))
  }
}
