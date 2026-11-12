import type { DecisionSignature } from './decision'
import type { Session, TerminalInfo, TerminalOutput } from './protocol'
import type { State } from './store'
import type { Link, TerminalRef } from './ws'
import type { FitAddon, Terminal } from './xterm'

/**
 * The most one input carries. A paste longer than this goes as several,
 * each signed, so every message fits the citadel's socket.
 */
export const INPUT_PIECE = 8 * 1024

/** How a conversation is shown: the chat with its agent, or a terminal in its worktree. */
export type ConvoMode = 'chat' | 'terminal'

/** What the terminals need from the page. */
export interface TerminalDeps {
  /** Signs opening a terminal, or says why this browser can't; nothing to sign before the world exists. */
  signOpen(x: Session, terminal: string): { signature?: DecisionSignature; error?: string }
  /** Signs one input, as signOpen. */
  signInput(x: Session, terminal: string, attach: string, seq: number, data: Uint8Array): { signature?: DecisionSignature; error?: string }
  /** Calls the citadel's API, signing in again if the sign-in lapsed. */
  api(path: string, init?: RequestInit): Promise<Response>
  /** Draws the page again, for a change only the terminals know about. */
  redraw(): void
}

/** Letters and digits, as terminal and attach ids are. */
export function newTerminalId(): string {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = crypto.getRandomValues(new Uint8Array(22))
  return Array.from(bytes, (b) => chars[b % chars.length]).join('')
}

/** Bytes as standard base64, as Go reads a []byte from JSON. */
export function toBase64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s)
}

/** Base64 back to bytes. Go writes an empty []byte as null, which is no bytes, not atob's "null". */
export function fromBase64(s: string | null | undefined): Uint8Array {
  const bin = atob(s ?? '')
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/** Splits input into pieces no longer than INPUT_PIECE. */
export function pieces(b: Uint8Array): Uint8Array[] {
  const out: Uint8Array[] = []
  for (let i = 0; i < b.length; i += INPUT_PIECE) out.push(b.subarray(i, i + INPUT_PIECE))
  return out
}

/**
 * Follows a terminal's output by byte offset: what a fetch of its screen
 * returned, then the live pieces after it. A piece that starts past what
 * it has means something was missed, and the screen is read again.
 */
export class OutputFollower {
  /** The offset of the next byte expected, or -1 until the screen is read. */
  next = -1
  private held: TerminalOutput[] = []

  /** Starts over from a screen just read, and returns what to draw: the screen, then whatever arrived meanwhile past it. */
  reset(screen: TerminalOutput): { draw: Uint8Array[]; gap: boolean } {
    const data = fromBase64(screen.data)
    this.next = screen.off + data.length
    const held = this.held
    this.held = []
    const draw = [data]
    for (const o of held) {
      const r = this.take(o)
      if (r.gap) return { draw, gap: true }
      if (r.draw) draw.push(r.draw)
    }
    return { draw, gap: false }
  }

  /** Waits for a screen to be read again, holding what arrives meanwhile. */
  hold(): void {
    this.next = -1
    this.held = []
  }

  /** What to draw of a live piece, or that something before it was missed. */
  push(o: TerminalOutput): { draw?: Uint8Array; gap: boolean } {
    if (this.next < 0) {
      this.held.push(o)
      return { gap: false }
    }
    return this.take(o)
  }

  private take(o: TerminalOutput): { draw?: Uint8Array; gap: boolean } {
    const data = fromBase64(o.data)
    const end = o.off + data.length
    if (end <= this.next) return { gap: false }
    if (o.off > this.next) return { gap: true }
    const draw = data.subarray(this.next - o.off)
    this.next = end
    return { draw, gap: false }
  }
}

type XTerm = typeof import('./xterm')

let xterm: Promise<XTerm> | null = null
function loadXterm(): Promise<XTerm> {
  // The font is measured once, as the terminal opens, so it must be here first.
  xterm ??= Promise.all([import('./xterm'), document.fonts?.load('13px "JetBrains Mono"').catch(() => undefined)]).then(([m]) => m)
  return xterm
}

const utf8 = new TextEncoder()

/** One terminal on screen: the emulator, what it has drawn, and what it has sent. */
class Pane {
  readonly el: HTMLElement
  private term: Terminal | null = null
  private fit: FitAddon | null = null
  private ro: ResizeObserver | null = null
  /** This view of the terminal, which counts its inputs from one. */
  private readonly attach = newTerminalId()
  private seq = 0
  private readonly follow = new OutputFollower()
  private reading = 0
  private shown = false
  private disposed = false
  /** What went wrong lately, shown under the screen. */
  note = ''

  constructor(
    readonly ref: TerminalRef,
    private readonly session: () => Session | undefined,
    private readonly link: () => Link,
    private readonly deps: TerminalDeps,
    private readonly changed: () => void,
  ) {
    this.el = document.createElement('div')
    this.el.className = 'term-screen'
    void this.boot()
  }

  private async boot(): Promise<void> {
    let m: XTerm
    try {
      m = await loadXterm()
    } catch (err) {
      this.say(`The terminal couldn’t be loaded: ${(err as Error).message}. Reload the page to try again.`)
      return
    }
    if (this.disposed) return
    const term = new m.Terminal({ fontFamily: '"JetBrains Mono", ui-monospace, monospace', fontSize: 13, cursorBlink: true, scrollback: 5000, theme: m.THEME, allowProposedApi: false })
    const fit = new m.FitAddon()
    term.loadAddon(fit)
    term.open(this.el)
    this.term = term
    this.fit = fit
    term.onData((d) => this.type(utf8.encode(d)))
    // Old mouse reports come as bytes, one per character.
    term.onBinary((d) => this.type(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)))
    term.onResize(({ cols, rows }) => this.link().terminalSize(this.ref, cols, rows))
    this.ro = new ResizeObserver(() => this.refit())
    this.ro.observe(this.el)
    this.refit()
    if (this.shown) {
      this.link().terminalSize(this.ref, term.cols, term.rows)
      void this.read()
      term.focus()
    }
  }

  /** The page shows this terminal now, so it follows it again from what it shows. */
  show(): void {
    this.shown = true
    if (!this.term) return
    this.refit()
    this.link().terminalSize(this.ref, this.term.cols, this.term.rows)
    void this.read()
    this.term.focus()
  }

  hide(): void {
    this.shown = false
    this.follow.hold()
  }

  /** Back in the page after the inbox was drawn again. */
  reattached(focus: boolean): void {
    if (!this.term) return
    this.refit()
    this.term.refresh(0, this.term.rows - 1)
    if (focus) this.term.focus()
  }

  focus(): void {
    this.term?.focus()
  }

  hasFocus(): boolean {
    return !!document.activeElement && this.el.contains(document.activeElement)
  }

  output(o: TerminalOutput): void {
    if (!this.term) return
    const r = this.follow.push(o)
    if (r.gap) {
      void this.read()
      return
    }
    if (r.draw?.length) this.term.write(r.draw)
  }

  /** Reads what the terminal shows from its host, and draws it in place of what this view had. */
  async read(): Promise<void> {
    const term = this.term
    if (!term) return
    const mine = ++this.reading
    this.follow.hold()
    const { host, id, terminal } = this.ref
    let screen: TerminalOutput
    try {
      const resp = await this.deps.api(`/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(id)}/terminals/${encodeURIComponent(terminal)}`)
      const out = (await resp.json().catch(() => ({}))) as TerminalOutput & { error?: string }
      if (!resp.ok) throw new Error(out.error ?? `The citadel answered ${resp.status}.`)
      screen = out
    } catch (err) {
      if (mine === this.reading && !this.disposed) this.say(`Couldn’t read the terminal from ${host}: ${(err as Error).message}`)
      return
    }
    if (mine !== this.reading || this.disposed) return
    const r = this.follow.reset(screen)
    term.reset()
    for (const d of r.draw) term.write(d)
    if (this.note.startsWith('Couldn’t read')) this.say('')
    if (r.gap) void this.read()
  }

  /** Sends what was typed, in pieces, each signed by this browser. */
  private type(data: Uint8Array): void {
    const x = this.session()
    if (!x) return
    for (const piece of pieces(data)) {
      const seq = ++this.seq
      const signed = this.deps.signInput(x, this.ref.terminal, this.attach, seq, piece)
      if (signed.error) {
        this.say(signed.error)
        return
      }
      if (!this.link().terminalInput(this.ref, { attach: this.attach, seq, data: toBase64(piece), signature: signed.signature })) {
        this.say('Not connected to the citadel, so that wasn’t sent. This page keeps retrying.')
        return
      }
    }
    if (this.note) this.say('')
  }

  private refit(): void {
    if (!this.fit || !this.el.isConnected || this.el.clientWidth === 0 || this.el.clientHeight === 0) return
    try {
      this.fit.fit()
    } catch {
      // Not laid out yet; the next resize fits it.
    }
  }

  private say(note: string): void {
    if (note === this.note) return
    this.note = note
    this.changed()
  }

  dispose(): void {
    this.disposed = true
    this.ro?.disconnect()
    this.term?.dispose()
    this.el.remove()
  }
}

/** What the inbox shows of one session's terminals right now. */
export interface TerminalView {
  /** The terminal shown, if one is open. */
  current?: TerminalInfo
  /** An opening on its way, or why the last one failed. */
  opening: boolean
  error: string
}

/**
 * Every terminal this page shows, each kept with what it has drawn while
 * the inbox is drawn again around it. The page follows one terminal at a
 * time, the one in sight.
 */
export class Terminals {
  link: Link | null = null
  private panes = new Map<string, Pane>()
  private opening = new Map<string, string>()
  private errors = new Map<string, string>()
  /** The terminal each session shows here, when it has more than one. */
  private chosen = new Map<string, string>()
  private sessions: Record<string, Session> = {}
  private shown: string | null = null
  private changes = 0

  constructor(private readonly deps: TerminalDeps) {}

  /** Moves on whenever what the inbox draws of terminals changes. */
  version(): number {
    return this.changes
  }

  private changed = (): void => {
    this.changes++
    this.deps.redraw()
  }

  private getLink = (): Link => {
    if (!this.link) throw new Error('no link to the citadel yet')
    return this.link
  }

  view(x: Session): TerminalView {
    const want = this.chosen.get(x.id)
    const current = x.terminals.find((t) => t.id === want) ?? x.terminals[x.terminals.length - 1]
    return { current, opening: this.opening.has(x.id), error: this.errors.get(x.id) ?? '' }
  }

  /** Shows one of a session's open terminals. */
  choose(x: Session, terminal: string): void {
    this.chosen.set(x.id, terminal)
    this.changed()
  }

  /** Opens a new terminal in a session's worktree, which the page shows once its island says it is open. */
  async open(x: Session): Promise<void> {
    if (this.opening.has(x.id)) return
    const terminal = newTerminalId()
    const signed = this.deps.signOpen(x, terminal)
    if (signed.error) {
      this.errors.set(x.id, signed.error)
      this.changed()
      return
    }
    this.opening.set(x.id, terminal)
    this.errors.delete(x.id)
    this.chosen.set(x.id, terminal)
    this.changed()
    let err = ''
    try {
      const resp = await this.deps.api(`/api/sessions/${encodeURIComponent(x.host)}/${encodeURIComponent(x.id)}/terminals`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ terminalId: terminal, signature: signed.signature }),
      })
      if (!resp.ok) err = ((await resp.json().catch(() => ({}))) as { error?: string }).error ?? `The citadel answered ${resp.status}.`
    } catch (e) {
      err = `Could not reach the citadel: ${(e as Error).message}`
    }
    this.opening.delete(x.id)
    if (err) this.errors.set(x.id, `Couldn’t open a terminal: ${err}`)
    this.changed()
  }

  /** Closes a terminal, ending its shell. Resolves with an error message, or empty. */
  async close(x: Session, terminal: string): Promise<string> {
    try {
      const resp = await this.deps.api(`/api/sessions/${encodeURIComponent(x.host)}/${encodeURIComponent(x.id)}/terminals/${encodeURIComponent(terminal)}/close`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
      if (resp.ok) return ''
      return ((await resp.json().catch(() => ({}))) as { error?: string }).error ?? `The citadel answered ${resp.status}.`
    } catch (e) {
      return `Could not reach the citadel: ${(e as Error).message}`
    }
  }

  /** The element that shows a terminal, the same one every time it is drawn. */
  screen(x: Session, terminal: string): HTMLElement {
    return this.pane(x, terminal).el
  }

  /** What went wrong lately in a terminal's view, if anything. */
  note(x: Session, terminal: string): string {
    return this.panes.get(key(x.host, x.id, terminal))?.note ?? ''
  }

  private pane(x: Session, terminal: string): Pane {
    const k = key(x.host, x.id, terminal)
    let p = this.panes.get(k)
    if (!p) {
      const ref = { host: x.host, id: x.id, terminal }
      p = new Pane(ref, () => this.sessions[x.id], this.getLink, this.deps, this.changed)
      this.panes.set(k, p)
      if (this.shown === k) p.show()
    }
    return p
  }

  /** Whether a terminal had the keyboard, before the inbox is drawn again. */
  focused(): string | null {
    for (const [k, p] of this.panes) if (p.hasFocus()) return k
    return null
  }

  /** After the inbox is drawn again: each terminal back on screen redrawn, and the keyboard back where it was. */
  reattached(focused: string | null): void {
    for (const [k, p] of this.panes) if (p.el.isConnected) p.reattached(k === focused)
  }

  focus(x: Session, terminal: string): void {
    this.panes.get(key(x.host, x.id, terminal))?.focus()
  }

  /**
   * Which terminal is in sight now, or none. The page follows that one
   * alone, so one that comes back into sight is read again from its host.
   */
  show(ref: TerminalRef | null): void {
    const k = ref ? key(ref.host, ref.id, ref.terminal) : null
    if (k === this.shown) return
    if (this.shown) this.panes.get(this.shown)?.hide()
    this.shown = k
    this.link?.watchTerminal(ref)
    if (k) this.panes.get(k)?.show()
  }

  output(ref: TerminalRef, out: TerminalOutput): void {
    const k = key(ref.host, ref.id, ref.terminal)
    if (k === this.shown) this.panes.get(k)?.output(out)
  }

  /** The socket came back, so the terminal in sight is read again for what it missed. */
  reconnected(): void {
    if (this.shown) void this.panes.get(this.shown)?.read()
  }

  /** Keeps up with the sessions, and lets go of the terminals that have closed. */
  update(sessions: Record<string, Session>): void {
    this.sessions = sessions
    for (const [k, p] of this.panes) {
      const x = sessions[p.ref.id]
      if (x?.host === p.ref.host && x.terminals.some((t) => t.id === p.ref.terminal)) continue
      p.dispose()
      this.panes.delete(k)
      if (this.shown === k) this.show(null)
    }
  }
}

function key(host: string, id: string, terminal: string): string {
  return `${host}/${id}/${terminal}`
}

/**
 * The terminal in sight: the open conversation's, in its terminal mode,
 * on a screen that shows the inbox.
 */
export function terminalInSight(s: State, terminals: Terminals): TerminalRef | null {
  if (s.phone || s.view !== 'inbox' || !s.inbox) return null
  const x = s.sessions[s.inbox]
  if (!x || convoMode(s, x) !== 'terminal') return null
  const t = terminals.view(x).current
  return t ? { host: x.host, id: x.id, terminal: t.id } : null
}

/** How a conversation is shown. A session that is over, with no terminal left open, shows its chat. */
export function convoMode(s: State, x: Session): ConvoMode {
  return s.convoMode[x.id] === 'terminal' && canTerminal(x) ? 'terminal' : 'chat'
}

/** Whether a session has, or can have, a terminal: it is open, or one is still open in it. */
export function canTerminal(x: Session): boolean {
  return x.status === 'running' || x.status === 'idle' || x.terminals.length > 0
}
