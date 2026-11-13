import { citadelAddress } from '../address'
import { myApprover, myKey, type ApproverActions } from '../approvers'
import { el } from '../dom'
import type { ApproverInfo, Rule } from '../protocol'
import { qrCode } from '../qr'
import { isOpen } from '../status'
import type { State } from '../store'
import { copyButton, factsBox, hint, note, timeNode } from './parts'

/**
 * What the Approvers page of Settings holds: every browser that can approve
 * what agents do in this world, a link that makes another browser one,
 * revocation, and whether this browser's storage keeps its key. Every
 * change is signed in this browser with its approver key, so only an
 * approver can make one.
 */
export function approversContent(s: State, actions: ApproverActions, keep: () => void): (Node | null)[] {
  if (!s.world?.ready) {
    return [
      el('p', { class: 'note' }, ['The world isn’t set up yet. The browser that sets it up, on the ', el('a', { href: '/setup', text: 'setup page' }), ', becomes its first approver.']),
    ]
  }
  if (!s.approvers) return [note('Waiting for the citadel to list the approvers.')]
  const byKey = new Map(s.approvers.map((a) => [a.key, a]))
  const nameOf = approverName(s)
  const me = myApprover(s)
  const canAct = !!me?.valid

  const kids: (Node | null)[] = []
  const valid = s.approvers.filter((a) => a.valid)
  kids.push(
    el(
      'div',
      { class: 'approvers' },
      valid.map((a) => approverRow(a, s, actions, canAct, nameOf)),
    ),
  )
  if (s.approverError) kids.push(el('p', { class: 'approver-error', role: 'alert', text: s.approverError }))

  if (canAct) {
    kids.push(s.invite ? inviteBox(s, actions) : el('div', {}, [el('button', { type: 'button', class: 'btn small', text: 'Add an approver', onclick: () => actions.invite() })]))
    kids.push(
      factsBox([
        [
          'This browser’s storage',
          s.persisted
            ? 'Kept'
            : el('span', { class: 'keyval' }, [el('span', { text: 'Best effort' }), el('button', { type: 'button', class: 'copy', text: 'Ask to keep', onclick: keep })]),
        ],
      ]),
    )
    if (s.persisted === false) kids.push(hint('The browser may clear this key if it runs short of space. Your recovery file, or another approver, brings it back.'))
  } else {
    kids.push(notAnApprover(s))
  }

  const gone = s.approvers.filter((a) => !a.valid)
  if (gone.length) {
    kids.push(
      el('details', { class: 'gone' }, [
        el('summary', { text: `${gone.length} no longer ${gone.length === 1 ? 'approves' : 'approve'}` }),
        ...gone.map((a) => el('div', { class: 'sub', text: `${a.name}: ${goneText(a, byKey, nameOf)}` })),
      ]),
    )
  }
  return kids
}

/** An approver's name by its key, "the owner key" for the owner's. */
function approverName(s: State): (key: string | undefined) => string {
  const byKey = new Map((s.approvers ?? []).map((a) => [a.key, a]))
  return (key) => (key === 'owner' ? 'the owner key' : (byKey.get(key ?? '')?.name ?? 'an unknown approver'))
}

function approverRow(a: ApproverInfo, s: State, actions: ApproverActions, canAct: boolean, nameOf: (key: string | undefined) => string): HTMLElement {
  const mine = a.key === myKey(s)
  const row = el('div', { class: 'row approver' }, [
    el('div', { class: 'grow' }, [
      el('span', { style: 'font-weight:600', text: a.name }),
      mine ? el('span', { class: 'me-tag', text: 'this browser' }) : null,
      el('span', { class: 'sub' }, ['Added ', timeNode(a.enrolled), a.by === 'owner' ? ' with the owner key' : ` by ${nameOf(a.by)}`]),
    ]),
    canAct && s.revoking !== a.key ? el('button', { type: 'button', class: 'copy', text: 'Revoke', 'aria-label': `Revoke ${a.name}`, onclick: () => actions.askRevoke(a.key) }) : null,
  ])
  if (!canAct || s.revoking !== a.key) return row
  // What goes along with it, and whether this browser does.
  const through = a.through ?? []
  const me = myKey(s)
  const loseMe = mine || (!!me && through.includes(me))
  // Everyone added through it, directly or by an approver it added, at any depth.
  const names = through.map((k) => (k === me ? 'this browser' : nameOf(k)))
  const left = (s.approvers ?? []).filter((x) => x.valid && x.key !== a.key && !through.includes(x.key)).length
  const ask = approvedInAdvance(s, new Set([a.key, ...through]))
  const lines = [
    names.length
      ? `Also removes ${names.length} ${names.length === 1 ? 'approver' : 'approvers'} added through it, directly or in turn: ${listText(names)}.`
      : '',
    ask.length ? `What ${names.length ? 'they' : 'it'} approved in advance will ask again until another approver approves it: ${listText(ask)}.` : '',
    loseMe ? 'This browser stops approving too, until it’s added again.' : '',
    left ? '' : 'No approver will be left: only your recovery file can add one after this.',
  ].filter(Boolean)
  return el('div', { class: 'approver confirming' }, [
    row,
    el('p', { class: 'sub', text: `Revoke ${a.name}? It stops approving for good. ${lines.join(' ')}`.trim() }),
    el('div', { class: 'actions' }, [
      el('button', { type: 'button', class: 'btn small danger', text: `Revoke ${a.name}`, onclick: () => actions.revoke(a.key) }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Cancel', onclick: () => actions.askRevoke(null) }),
    ]),
  ])
}

/**
 * What the given approvers approved in advance and revoking them ends, as
 * counted phrases: trust they turned on in open sessions, and rule lists
 * with allow rules that stand on their signature.
 */
function approvedInAdvance(s: State, keys: Set<string>): string[] {
  const allows = (rules: Rule[]): boolean => rules.some((r) => r.decision === 'allow')
  const open = Object.values(s.sessions).filter(isOpen)
  const trusted = open.filter((x) => x.trusted && !!x.trustApprover && keys.has(x.trustApprover)).length
  const lists =
    Object.values(s.repos).filter((r) => !r.rulesLapsed && !!r.rulesApprover && keys.has(r.rulesApprover) && allows(r.rules)).length +
    open.filter((x) => !x.rulesLapsed && !!x.rulesApprover && keys.has(x.rulesApprover) && allows(x.rules)).length
  const out: string[] = []
  if (trusted) out.push(`${trusted} trusted ${trusted === 1 ? 'session' : 'sessions'}`)
  if (lists) out.push(`${lists} ${lists === 1 ? 'rule list' : 'rule lists'}`)
  return out
}

/** The "Add an approver" link, as a QR code and a link, until it is used or expires. */
function inviteBox(s: State, actions: ApproverActions): HTMLElement {
  const inv = s.invite!
  const used = s.approvers?.find((a) => a.voucher === inv.claim)
  const expired = !used && Date.now() > Date.parse(inv.expires)
  const close = el('button', { type: 'button', class: 'btn small ghost', text: used || expired ? 'Done' : 'Cancel', onclick: () => actions.closeInvite() })
  if (used) {
    return el('div', { class: 'invite' }, [el('p', { text: `${used.name} now approves for this world.` }), el('div', { class: 'actions' }, [close])])
  }
  if (expired) {
    return el('div', { class: 'invite' }, [
      el('p', { text: 'That link expired before anyone used it.' }),
      el('div', { class: 'actions' }, [el('button', { type: 'button', class: 'btn small', text: 'Make another', onclick: () => actions.invite() }), close]),
    ])
  }
  return el('div', { class: 'invite' }, [
    el('p', { class: 'sub', text: `Open this on the phone or browser to add: scan the code, or copy the link to it. It works once, until ${new Date(inv.expires).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}.` }),
    addressChoice(s, actions),
    inv.address !== location.origin
      ? el('p', { class: 'sub', text: `The browser you add approves at ${inv.address}, so it must reach the citadel there.` })
      : null,
    qrCode(inv.link, 'QR code of the link that adds an approver'),
    el('div', { class: 'addhost' }, [el('pre', {}, [el('code', { text: inv.link })])]),
    el('p', { class: 'sub', text: 'Anyone who opens it first becomes an approver, so share it only with yourself.' }),
    el('div', { class: 'actions' }, [copyButton(inv.link, 'Copy the link that adds an approver', 'btn small'), close]),
  ])
}

/**
 * Where the link opens the world: this page's address, the citadel's
 * --url, the address the link names now, or another one typed in.
 */
function addressChoice(s: State, actions: ApproverActions): HTMLElement {
  const inv = s.invite!
  const OTHER = 'other'
  const choices: [string, string][] = [[location.origin, `${location.origin} (this page)`]]
  if (s.world?.url && s.world.url !== location.origin) choices.push([s.world.url, `${s.world.url} (the citadel\u2019s address)`])
  if (!choices.some(([v]) => v === inv.address)) choices.push([inv.address, inv.address])
  choices.push([OTHER, 'Another address\u2026'])
  const select = el('select', { 'aria-label': 'Address the link opens at' }) as HTMLSelectElement
  select.replaceChildren(...choices.map(([value, text]) => el('option', { value, text })))
  select.value = inv.other ? OTHER : inv.address
  select.addEventListener('change', () => {
    if (select.value === OTHER) actions.inviteOther({ text: '', error: '' })
    else actions.inviteAt(select.value)
  })
  const box = el('div', { class: 'invite-address' }, [el('label', { class: 'chip' }, ['Opens at', select])])
  const other = inv.other
  if (other) {
    const input = el('input', { type: 'url', value: other.text, placeholder: 'https://box.example.ts.net', 'aria-label': 'Another address', autocomplete: 'off' }) as HTMLInputElement
    const apply = (): void => {
      const got = citadelAddress(input.value)
      if ('error' in got) actions.inviteOther({ text: input.value, error: got.error })
      else actions.inviteAt(got.address)
    }
    input.addEventListener('input', () => {
      other.text = input.value
    })
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') apply()
    })
    box.appendChild(el('div', { class: 'actions' }, [input, el('button', { type: 'button', class: 'btn small', text: 'Use', onclick: apply })]))
    if (other.error) box.appendChild(el('p', { class: 'approver-error', role: 'alert', text: other.error }))
    queueMicrotask(() => input.focus())
  }
  return box
}

/** Why this browser doesn't approve for the world, and what it can do about it. */
export function notAnApprover(s: State): HTMLElement {
  const me = myApprover(s)
  const nameOf = approverName(s)
  const why = !me
    ? 'This browser isn’t an approver.'
    : me.revoked
      ? me.revokedBy === me.key
        ? 'This browser revoked its own key.'
        : `This browser’s key was revoked by ${nameOf(me.revokedBy)}.`
      : 'This browser’s key was removed along with the approver that added it.'
  return el('p', { class: 'muted', style: 'margin:0;font-size:13px' }, [
    `${why} To make it one, open an “Add an approver” link from an approver, or use your recovery file on the `,
    el('a', { href: '/setup', text: 'setup page' }),
    '.',
  ])
}

/** Why an approver no longer approves. */
function goneText(a: ApproverInfo, byKey: Map<string, ApproverInfo>, nameOf: (key: string | undefined) => string): string {
  if (a.revoked) return a.revokedBy === a.key ? 'revoked by itself' : `revoked by ${nameOf(a.revokedBy)}`
  for (let up = byKey.get(a.by); up; up = byKey.get(up.by)) {
    if (up.revoked) return `removed with ${up.name}`
  }
  return 'removed'
}

/** "A", "A and B", or "A, B, and C". */
function listText(items: string[]): string {
  if (items.length < 3) return items.join(' and ')
  return `${items.slice(0, -1).join(', ')}, and ${items[items.length - 1]}`
}
