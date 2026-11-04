import { myApprover, myKey, type ApproverActions } from '../approvers'
import { el } from '../dom'
import type { ApproverInfo } from '../protocol'
import { qrCode } from '../qr'
import type { State } from '../store'
import { copyButton, factsBox, foldSection, hint, timeNode } from './parts'

/**
 * The Approvers section of the citadel's panel: every browser that can
 * approve what agents do in this world, a link that makes another browser
 * one, and revocation. Every change is signed in this browser with its
 * approver key, so only an approver can make one.
 */
export function approversSection(s: State, actions: ApproverActions, keepStorage: () => void): HTMLElement | null {
  if (!s.world?.ready || !s.approvers) return null
  const byKey = new Map(s.approvers.map((a) => [a.key, a]))
  const nameOf = (key: string | undefined): string => (key === 'owner' ? 'the owner key' : (byKey.get(key ?? '')?.name ?? 'an unknown approver'))
  const me = myApprover(s)
  const canAct = !!me?.valid

  const kids: (Node | null)[] = [hint('Browsers that can approve what agents do in this world.')]
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
            : el('span', { class: 'keyval' }, [el('span', { text: 'Best effort' }), el('button', { type: 'button', class: 'copy', text: 'Ask to keep', onclick: keepStorage })]),
        ],
      ]),
    )
    if (s.persisted === false) kids.push(hint('The browser may clear this key if it runs short of space. Your recovery file, or another approver, brings it back.'))
  } else {
    kids.push(notAnApprover(me, nameOf))
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
  return foldSection('approvers', 'Approvers', kids, valid.length)
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
  const lines = [
    names.length
      ? `Also removes ${names.length} ${names.length === 1 ? 'approver' : 'approvers'} added through it, directly or in turn: ${listText(names)}.`
      : '',
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
    qrCode(inv.link, 'QR code of the link that adds an approver'),
    el('div', { class: 'addhost' }, [el('pre', {}, [el('code', { text: inv.link })])]),
    el('p', { class: 'sub', text: 'Anyone who opens it first becomes an approver, so share it only with yourself.' }),
    el('div', { class: 'actions' }, [copyButton(inv.link, 'Copy the link that adds an approver', 'btn small'), close]),
  ])
}

/** What this browser can do when it doesn't approve for the world. */
function notAnApprover(me: ApproverInfo | null, nameOf: (key: string | undefined) => string): HTMLElement {
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
