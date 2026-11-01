import { el } from './dom'
import type { Session } from './protocol'

export interface ReplyActions {
  /** Sends another prompt to an idle session. Resolves with an error message, or empty on success. */
  prompt(text: string): Promise<string>
  /** Interrupts the turn in progress and leaves the session open. */
  cancel(): Promise<string>
  /** Ends the session. Resolves with an error message, or empty on success. */
  stop(): Promise<string>
}

/** Unsent replies by session, so a rebuild keeps what was typed. */
const drafts = new Map<string, string>()

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
  const send = el('button', { type: 'button', class: 'btn small primary', text: 'Send' }) as HTMLButtonElement
  send.disabled = !idle
  // While the agent works, Interrupt stops the turn and keeps the session;
  // End session is the way out in either state.
  const cancel = el('button', { type: 'button', class: 'btn small', text: s.cancelling ? 'Stopping\u2026' : 'Interrupt', title: 'Stop what the agent is doing and keep the conversation' }) as HTMLButtonElement
  cancel.hidden = s.status !== 'running'
  cancel.disabled = !!s.cancelling
  const stop = el('button', { type: 'button', class: 'btn small ghost', text: 'End session' }) as HTMLButtonElement
  const note = el('span', { class: 'sub' })
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
  const hint = el('span', { class: 'muted', style: 'font-size:12px', text: idle ? '\u2318 Enter sends' : '' })
  return el('div', { class: 'reply' }, [input, el('div', { class: 'acts' }, [send, cancel, stop, hint, note])])
}
