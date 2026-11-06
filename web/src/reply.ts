import { el } from './dom'
import type { AgentCommand, Session } from './protocol'

export interface ReplyActions {
  /** Sends another prompt to an idle session. Resolves with an error message, or empty on success. */
  prompt(text: string): Promise<string>
  /**
   * The slash commands the session's agent announced, less the ones the
   * host withholds, or an error message.
   */
  commands(): Promise<AgentCommand[] | string>
  /** Interrupts the turn in progress and leaves the session open. */
  cancel(): Promise<string>
  /** Ends the session. Resolves with an error message, or empty on success. */
  stop(): Promise<string>
}

/** Unsent replies by session, so a rebuild keeps what was typed. */
const drafts = new Map<string, string>()

/** Each session's commands as last fetched, and when, kept across rebuilds. */
const commandsBySession = new Map<string, { at: number; commands: Promise<AgentCommand[] | string> }>()

/** How long a session's fetched commands are used before they are read again, as when a plugin is added. */
const COMMANDS_FOR_MS = 60_000

/** The command name a reply is still typing: its first word, while that is a slash and a name with nothing after it yet. */
function typingCommand(value: string, caret: number): string | null {
  const m = /^\/([A-Za-z0-9_:-]*)$/.exec(value.slice(0, caret))
  return m && !/\s/.test(value.slice(caret, caret + 1) || '') ? m[1] : null
}

/** The command a reply starts with, as the daemon reads it: a slash and a name, then whitespace or nothing. */
function commandOf(value: string): string | null {
  const m = /^\s*\/([A-Za-z0-9_:-]+)(\s|$)/.exec(value)
  return m ? m[1] : null
}

/** The reply box and stop button for an open session. */
export function replyBox(s: Session, actions: ReplyActions): HTMLElement {
  const idle = s.status === 'idle'
  const input = el('textarea', {
    placeholder: idle ? 'Reply to the agent\u2026' : 'The agent is working. You can reply when it stops.',
    'aria-label': 'Reply',
  }) as HTMLTextAreaElement
  input.disabled = !idle
  input.value = drafts.get(s.id) ?? ''
  input.addEventListener('input', () => drafts.set(s.id, input.value))

  // Typing a slash at the start lists the agent's commands, filtered as
  // the name is typed. Picking one puts it in the box, and the line below
  // says what the command takes.
  const menuId = `commands-${s.id}`
  const menu = el('div', { class: 'cmd-menu', id: menuId, role: 'listbox', 'aria-label': 'The agent\u2019s commands' })
  menu.hidden = true
  input.setAttribute('aria-autocomplete', 'list')
  input.setAttribute('aria-controls', menuId)
  let shown: AgentCommand[] = []
  let active = 0
  let known: AgentCommand[] = []
  const commands = (): Promise<AgentCommand[] | string> => {
    const cached = commandsBySession.get(s.id)
    if (cached && Date.now() - cached.at < COMMANDS_FOR_MS) return cached.commands
    const fetched = actions.commands()
    commandsBySession.set(s.id, { at: Date.now(), commands: fetched })
    // An error isn't kept, so the next slash asks again.
    void fetched.then((r) => {
      if (typeof r === 'string') commandsBySession.delete(s.id)
    })
    return fetched
  }
  const close = (): void => {
    menu.hidden = true
    shown = []
    input.removeAttribute('aria-activedescendant')
  }
  const pick = (c: AgentCommand): void => {
    input.value = `/${c.name} `
    drafts.set(s.id, input.value)
    close()
    input.focus()
    input.setSelectionRange(input.value.length, input.value.length)
    describe()
  }
  const highlight = (): void => {
    menu.querySelectorAll('[role="option"]').forEach((n, i) => n.setAttribute('aria-selected', i === active ? 'true' : 'false'))
    const id = shown.length ? `${menuId}-${active}` : null
    if (id) {
      input.setAttribute('aria-activedescendant', id)
      document.getElementById(id)?.scrollIntoView({ block: 'nearest' })
    } else input.removeAttribute('aria-activedescendant')
  }
  const show = async (): Promise<void> => {
    const typed = typingCommand(input.value, input.selectionStart ?? input.value.length)
    if (typed === null || input.disabled) {
      close()
      return
    }
    const got = await commands()
    // The reply may have changed while the commands were fetched.
    if (typingCommand(input.value, input.selectionStart ?? input.value.length) !== typed) return
    if (typeof got === 'string') {
      menu.replaceChildren(el('div', { class: 'cmd-none', text: `The agent\u2019s commands can\u2019t be listed: ${got}` }))
      shown = []
      menu.hidden = false
      return
    }
    known = got
    const t = typed.toLowerCase()
    shown = [...got.filter((c) => c.name.toLowerCase().startsWith(t)), ...got.filter((c) => !c.name.toLowerCase().startsWith(t) && c.name.toLowerCase().includes(t))]
    active = 0
    menu.replaceChildren(
      ...(shown.length
        ? shown.map((c, i) =>
            el('div', { class: 'cmd', id: `${menuId}-${i}`, role: 'option', 'aria-selected': 'false', title: c.description || null, onmousedown: (e) => { e.preventDefault(); pick(c) } }, [
              el('strong', { text: `/${c.name}` }),
              c.description ? el('span', { class: 'muted', text: c.description }) : null,
            ]),
          )
        : [el('div', { class: 'cmd-none', text: got.length ? `No command of the agent\u2019s starts with /${typed}. The host refuses one it doesn\u2019t know.` : 'The agent hasn\u2019t announced any commands.' })]),
    )
    menu.hidden = false
    highlight()
  }
  const describe = (): void => {
    const name = commandOf(input.value)
    const c = name ? known.find((k) => k.name.toLowerCase() === name.toLowerCase()) : undefined
    hint.textContent = c?.hint ? `/${c.name} takes: ${c.hint}` : idle ? '\u2318 Enter sends' : ''
  }
  input.addEventListener('input', () => {
    void show()
    describe()
  })
  input.addEventListener('click', () => void show())
  input.addEventListener('blur', () => close())
  input.addEventListener('keydown', (e) => {
    if (menu.hidden || !shown.length) {
      if (e.key === 'Escape' && !menu.hidden) close()
      return
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      active = (active + (e.key === 'ArrowDown' ? 1 : shown.length - 1)) % shown.length
      highlight()
    } else if ((e.key === 'Enter' && !e.metaKey && !e.ctrlKey) || e.key === 'Tab') {
      e.preventDefault()
      pick(shown[active])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      close()
    }
  })
  const send = el('button', { type: 'button', class: 'btn small primary', text: 'Send' }) as HTMLButtonElement
  send.disabled = !idle
  // While the agent works, Interrupt stops the turn and keeps the session;
  // End session is the way out in either state.
  const cancel = el('button', { type: 'button', class: 'btn small', text: s.cancelling ? 'Stopping\u2026' : 'Interrupt', title: 'Stop what the agent is doing and keep the conversation' }) as HTMLButtonElement
  cancel.hidden = s.status !== 'running'
  cancel.disabled = !!s.cancelling
  const stop = el('button', { type: 'button', class: 'btn small ghost', text: 'End session' }) as HTMLButtonElement
  const note = el('span', { class: 'sub' })
  const hint = el('span', { class: 'muted', style: 'font-size:12px', text: idle ? '\u2318 Enter sends' : '' })
  const submit = async (): Promise<void> => {
    const text = input.value.trim()
    if (!text || send.disabled) return
    send.disabled = true
    input.disabled = true
    note.textContent = 'Sending\u2026'
    const err = await actions.prompt(text)
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      send.disabled = false
      input.disabled = false
    } else {
      note.textContent = ''
      drafts.delete(s.id)
      input.value = ''
    }
  }
  send.addEventListener('click', () => void submit())
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      void submit()
    }
  })
  cancel.addEventListener('click', async () => {
    cancel.disabled = true
    note.textContent = 'Interrupting\u2026'
    note.style.color = ''
    const err = await actions.cancel()
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      cancel.disabled = false
    }
  })
  stop.addEventListener('click', async () => {
    stop.disabled = true
    note.textContent = 'Ending\u2026'
    note.style.color = ''
    const err = await actions.stop()
    if (err) {
      note.textContent = err
      note.style.color = 'var(--bad)'
      stop.disabled = false
    }
  })
  return el('div', { class: 'reply' }, [el('div', { class: 'cmd-anchor' }, [menu, input]), el('div', { class: 'acts' }, [send, cancel, stop, hint, note])])
}
