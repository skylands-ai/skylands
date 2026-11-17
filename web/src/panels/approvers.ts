import { citadelAddress, cliCommand } from '../address'
import { approverTree, descendantCount, mayChange, myApprover, myKey, type ApproverActions, type ApproverNode } from '../approvers'
import { el, reduceMotion } from '../dom'
import type { ApproverInfo, Rule } from '../protocol'
import { qrCode } from '../qr'
import { isOpen } from '../status'
import type { Renaming, Revoking, State } from '../store'
import { copyButton, factsBox, hint, note, timeNode } from './parts'

/**
 * What the Approvers page of Settings holds: every browser and host's CLI
 * that can approve what agents do in this world, as a graph of who added
 * whom, a link that makes another browser or CLI one, and whether this
 * browser's storage keeps its key. Renaming and revoking are in each
 * approver's details, beside the graph. Every change is signed in this browser with its
 * approver key, so only an approver can make one.
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
  kids.push(approverGraph(approverTree(s.approvers), s, actions))
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

/** The tags an approver carries: that it is this browser, and that it is a host's CLI. */
function approverTags(a: ApproverInfo, mine: boolean): HTMLElement | null {
  if (!mine && a.device !== 'cli') return null
  return el('span', { class: 'tags' }, [
    mine ? el('span', { class: 'me-tag', text: 'this browser' }) : null,
    a.device === 'cli' ? el('span', { class: 'device-tag', text: 'CLI', title: 'A host’s CLI, which signs what you decide on that host' }) : null,
  ])
}

/**
 * The approvers that approve, drawn top down: those the owner key added
 * along the top, and under each one, joined by lines, the approvers its
 * links added. Each approver with descendants has a button that hides
 * them or shows them again. An approver opens its details beside the
 * graph, and opening it again closes them.
 */
function approverGraph(roots: ApproverNode[], s: State, actions: ApproverActions): HTMLElement {
  const folded = new Set(s.foldedApprovers)
  const me = myKey(s)
  const branch = (n: ApproverNode): HTMLElement => {
    const a = n.approver
    const open = s.approverDetails === a.key
    const kids: (Node | null)[] = [
      el(
        'button',
        {
          type: 'button',
          class: 'graph-node',
          'data-keep': `approver-${a.key}`,
          'aria-expanded': String(open),
          'aria-controls': open ? DETAILS_ID : null,
          onclick: () => (open ? closeDetails(actions) : actions.select(a.key)),
        },
        [el('span', { class: 'name', title: a.name, text: a.name }), approverTags(a, a.key === me)],
      ),
    ]
    if (n.children.length) {
      const hidden = folded.has(a.key)
      kids.push(
        el('div', { class: 'graph-stem' }, [
          el('button', {
            type: 'button',
            class: 'graph-fold',
            'data-keep': `fold-${a.key}`,
            'aria-expanded': String(!hidden),
            'aria-label': `Approvers added through ${a.name}`,
            text: hidden ? `+${descendantCount(n)}` : '−',
            onclick: () => actions.fold(a.key, !hidden),
          }),
        ]),
      )
      if (!hidden) kids.push(el('ul', {}, n.children.map(branch)))
    }
    return el('li', {}, kids)
  }
  const graph = el('div', { class: 'approver-graph' }, [el('ul', { class: 'graph-roots', 'aria-label': 'Approvers' }, roots.map(branch))])
  graph.addEventListener('keydown', escapeCloses(actions))
  return graph
}

const DETAILS_ID = 'approver-details'

/** The approver whose details were drawn last, so the panel slides in only as it opens. */
let drawnDetails: string | null = null

/**
 * The panel beside the graph with the details of the approver opened in
 * it, and renaming and revoking it where this browser may, or null when
 * none is open.
 */
export function approverDetails(s: State, actions: ApproverActions): HTMLElement | null {
  const a = s.world?.ready && s.approverDetails ? s.approvers?.find((x) => x.key === s.approverDetails && x.valid) : undefined
  if (!a) {
    drawnDetails = null
    return null
  }
  const entering = drawnDetails === null
  drawnDetails = a.key
  const nameOf = approverName(s)
  // The approvers above it, nearest first, for the keyboard to go back to
  // when the graph hides it.
  const byKey = new Map((s.approvers ?? []).map((x) => [x.key, x]))
  const above: string[] = []
  for (let up = byKey.get(a.by); up && !above.includes(up.key); up = byKey.get(up.by)) above.push(up.key)
  const aside = el(
    'aside',
    {
      id: DETAILS_ID,
      class: `approver-details${entering ? ' entering' : ''}`,
      'data-approver': a.key,
      'data-above': above.join(' '),
      'data-keep': DETAILS_ID,
      tabindex: '-1',
      'aria-labelledby': `${DETAILS_ID}-name`,
    },
    [
      el('div', { class: 'details-head' }, [
        el('h3', { id: `${DETAILS_ID}-name`, text: a.name }),
        el('button', { type: 'button', class: 'details-close', 'aria-label': 'Close', text: '×', onclick: () => closeDetails(actions) }),
      ]),
      approverTags(a, a.key === myKey(s)),
      factsBox([
        ['Added', timeNode(a.enrolled)],
        ['Added by', a.by === 'owner' ? 'The owner key' : nameOf(a.by)],
      ]),
      mayChange(s, a.key) ? detailActions(a, s, actions, nameOf) : null,
    ],
  )
  aside.addEventListener('keydown', escapeCloses(actions))
  if (entering) {
    // The panel narrows the page as it opens, which can leave the approver
    // it shows out of sight, so the graph scrolls it back once it's open.
    const inSight = (): void => document.querySelector(`[data-keep="${CSS.escape(`approver-${a.key}`)}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    queueMicrotask(() => {
      aside.focus({ preventScroll: true })
      if (reduceMotion) inSight()
      else aside.addEventListener('animationend', inSight, { once: true })
    })
  }
  return aside
}

/** Escape closes the details panel, and goes no further. */
function escapeCloses(actions: ApproverActions): (e: KeyboardEvent) => void {
  return (e) => {
    if (e.key !== 'Escape' || !document.getElementById(DETAILS_ID)) return
    e.stopPropagation()
    closeDetails(actions)
  }
}

/**
 * Slides the details panel out and closes it, giving the keyboard back to
 * its approver in the graph if the panel had it, or to the nearest one
 * above it while the graph hides it. It closes only if the same
 * approver's details still show by then.
 */
function closeDetails(actions: ApproverActions): void {
  const aside = document.getElementById(DETAILS_ID)
  if (!aside) return actions.select(null)
  const key = aside.dataset.approver ?? ''
  const back = [key, ...(aside.dataset.above ?? '').split(' ').filter(Boolean)]
  const hadKeyboard = aside.contains(document.activeElement)
  let done = false
  const finish = (): void => {
    if (done) return
    done = true
    if (document.getElementById(DETAILS_ID)?.dataset.approver !== key) return
    actions.select(null)
    if (!hadKeyboard) return
    for (const k of back) {
      const node = document.querySelector<HTMLElement>(`[data-keep="${CSS.escape(`approver-${k}`)}"]`)
      if (node) return node.focus()
    }
  }
  if (reduceMotion) return finish()
  if (aside.classList.contains('leaving')) return
  aside.classList.remove('entering')
  aside.classList.add('leaving')
  aside.addEventListener('animationend', finish, { once: true })
  // Drawn again meanwhile, the panel loses its animation, so this closes it.
  setTimeout(finish, 400)
}

/** Renaming and revoking an approver this browser may change, each asked for first. */
function detailActions(a: ApproverInfo, s: State, actions: ApproverActions, nameOf: (key: string | undefined) => string): HTMLElement {
  if (s.renaming?.key === a.key) return renameBox(a, s.renaming, actions)
  if (s.revoking?.key === a.key) return revokeBox(a, s, s.revoking, actions, nameOf)
  return el('div', { class: 'details-actions' }, [
    el('button', { type: 'button', class: 'btn small', text: 'Rename', 'aria-label': `Rename ${a.name}`, 'data-keep': `rename-${a.key}`, onclick: () => actions.askRename(a.key) }),
    el('button', { type: 'button', class: 'btn small', text: 'Revoke', 'aria-label': `Revoke ${a.name}`, 'data-keep': `revoke-${a.key}`, onclick: () => actions.askRevoke(a.key) }),
  ])
}

/**
 * What revoking an approver takes along, and whether this browser goes
 * too, with the button that revokes it. Cancel has the keyboard first.
 */
function revokeBox(a: ApproverInfo, s: State, r: Revoking, actions: ApproverActions, nameOf: (key: string | undefined) => string): HTMLElement {
  // Everyone added through it, directly or by an approver it added, at any depth.
  const through = a.through ?? []
  const me = myKey(s)
  const loseMe = a.key === me || (!!me && through.includes(me))
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
  return el('div', { class: 'details-box confirming' }, [
    el('p', { text: `Revoke ${a.name}? It stops approving for good. ${lines.join(' ')}`.trim() }),
    el('div', { class: 'details-actions' }, [
      el('button', { type: 'button', class: 'btn small danger', text: `Revoke ${a.name}`, onclick: () => actions.revoke(a.key) }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Cancel', 'data-keep': `revoke-${a.key}`, onclick: () => actions.askRevoke(null) }),
    ]),
    r.error ? el('p', { class: 'approver-error', role: 'alert', text: r.error }) : null,
  ])
}

/** The name being typed for an approver being renamed. */
function renameBox(a: ApproverInfo, r: Renaming, actions: ApproverActions): HTMLElement {
  const input = el('input', { type: 'text', value: r.text, maxlength: '80', autocomplete: 'off', 'aria-label': `New name for ${a.name}`, 'data-keep': `renaming-${a.key}` })
  input.addEventListener('input', () => {
    r.text = input.value
  })
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') actions.rename()
    else if (e.key === 'Escape') {
      // Escape stops renaming, and leaves the panel open.
      e.stopPropagation()
      actions.askRename(null)
    }
  })
  // Opening the box puts the keyboard in it with the name selected; drawn
  // again while typing, it already has the keyboard back.
  queueMicrotask(() => {
    if (document.activeElement === input) return
    input.focus()
    input.select()
  })
  return el('div', { class: 'details-box renaming' }, [
    input,
    el('div', { class: 'details-actions' }, [
      el('button', { type: 'button', class: 'btn small', text: 'Save', disabled: r.saving ? '' : null, onclick: () => actions.rename() }),
      el('button', { type: 'button', class: 'btn small ghost', text: 'Cancel', 'data-keep': `rename-${a.key}`, onclick: () => actions.askRename(null) }),
    ]),
    r.error ? el('p', { class: 'approver-error', role: 'alert', text: r.error }) : null,
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
    el('p', { class: 'sub', text: 'To make a host’s CLI an approver instead, so it can sign what you decide there, run this on the host:' }),
    el('div', { class: 'addhost' }, [el('pre', {}, [el('code', { text: cliCommand(inv.link) })]), copyButton(cliCommand(inv.link), 'Copy the command that makes a host’s CLI an approver')]),
    el('p', { class: 'sub', text: 'Anyone who uses it first becomes an approver, so share it only with yourself.' }),
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
