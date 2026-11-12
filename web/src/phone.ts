import { el } from './dom'
import { approvalCard, questionCard, type InboxActions } from './inbox'
import { who } from './notify'
import { timeNode } from './panels/parts'
import { notifySupport } from './notify'
import { notifySwitch } from './panels/world'
import { inputsVersion } from './requestinput'
import { inboxPath, type InboxRoute } from './route'
import { isStale, sessionLabel } from './status'
import { pendingItems, type PendingItem, type State } from './store'

/** What the phone view does beyond answering: going to another route, back to the list, and turning notifications on or off. */
export interface PhoneActions extends InboxActions {
  go(route: InboxRoute): void
  back(): void
  setNotify(on: boolean): void
}

function routeOf(i: PendingItem): InboxRoute {
  return { host: i.x.host, session: i.x.id, item: { kind: i.kind, id: i.id } }
}

/** A link within the phone view: a real link, so it can be copied, that moves without loading the page. */
function link(route: InboxRoute, actions: PhoneActions, attrs: Record<string, string>, children: (Node | string | null)[]): HTMLElement {
  return el('a', { ...attrs, href: inboxPath(route), onclick: (ev: Event) => {
    const e = ev as MouseEvent
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
    e.preventDefault()
    actions.go(route)
  } }, children)
}

function backText(s: State): string {
  const n = pendingItems(s).length
  return `‹ Everything waiting${n ? ` (${n})` : ''}`
}

function backLink(s: State, actions: PhoneActions): HTMLElement {
  return el('a', { class: 'phone-back', href: '/inbox', text: backText(s), onclick: (e: Event) => {
    e.preventDefault()
    actions.back()
  } })
}

function card(s: State, i: PendingItem, actions: PhoneActions): HTMLElement {
  return link(routeOf(i), actions, { class: `phone-card ${i.kind}` }, [
    el('span', { class: 'mark' }),
    el('span', { class: 'body' }, [
      el('strong', { text: i.title }),
      el('span', { class: 'muted', text: who(s, i.x) }),
      el('span', { class: 'muted' }, [i.kind === 'question' ? 'asked ' : 'waiting ', timeNode(i.at, 'ago')]),
    ]),
  ])
}

function list(s: State, route: InboxRoute, actions: PhoneActions): HTMLElement[] {
  const x = route.session ? s.sessions[route.session] : undefined
  const items = pendingItems(s, route.session)
  const out: HTMLElement[] = []
  if (route.session) {
    out.push(backLink(s, actions))
    out.push(el('h1', { text: x ? `Waiting in “${sessionLabel(x, 60)}”` : 'Waiting in this conversation' }))
    if (x) out.push(el('p', { class: 'muted', text: who(s, x) }))
  } else {
    out.push(el('h1', {}, ['Waiting on you', items.length ? el('span', { class: 'badge', text: String(items.length) }) : null]))
  }
  if (isStale(s)) out.push(el('p', { class: 'phone-stale', text: 'The citadel can’t be reached, so this is what was waiting when it last could.' }))
  else if (s.loading) out.push(el('p', { class: 'phone-stale', text: 'The citadel is catching up on what happened, so this may change once it has.' }))
  if (!items.length) {
    out.push(el('p', { class: 'phone-empty', text: route.session ? 'Nothing in this conversation is waiting on you.' : 'Nothing is waiting on you. When an agent asks for approval or has a question, it shows here.' }))
  }
  out.push(el('div', { class: 'phone-cards' }, items.map((i) => card(s, i, actions))))
  if (!route.session) out.push(el('div', { class: 'phone-notify' }, [notifySwitch(s, actions.setNotify)]))
  return out
}

function item(s: State, route: InboxRoute, actions: PhoneActions): HTMLElement[] {
  const x = route.session ? s.sessions[route.session] : undefined
  const want = route.item
  const p = want?.kind === 'request' ? x?.pending.find((r) => r.requestId === want.id) : undefined
  const q = want?.kind === 'question' ? x?.questions.find((r) => r.questionId === want.id) : undefined
  const out: HTMLElement[] = [backLink(s, actions)]
  if (!x || (!p && !q)) {
    out.push(el('p', { class: 'phone-empty', text: `This ${want?.kind === 'question' ? 'question' : 'request'} is no longer waiting: it was answered, here or somewhere else, or it expired, or its session ended.` }))
    return out
  }
  out.push(el('p', { class: 'muted phone-where', text: who(s, x) }))
  out.push(el('h1', { text: sessionLabel(x, 120) }))
  if (isStale(s)) out.push(el('p', { class: 'phone-stale', text: 'The citadel can’t be reached, so this may already be answered, and an answer from here won’t arrive until it can.' }))
  else if (s.loading) out.push(el('p', { class: 'phone-stale', text: 'The citadel is catching up on what happened, so this may already be answered.' }))
  if (p) {
    // The card is all the phone shows, so the input being approved is shown
    // open, where the desktop folds it.
    const c = approvalCard(s, x, p, actions)
    c.querySelector('details:not(.remember)')?.setAttribute('open', '')
    out.push(c)
  } else out.push(questionCard(s, x, q!, actions))
  return out
}

let lastDeps: unknown[] | null = null

/**
 * The phone's view: only what waits on a person, as a list of cards, and
 * one tool call or question at a time, answered with the same card the
 * desktop's conversation shows. It is rebuilt only when something it shows
 * has changed.
 */
export function renderPhone(root: HTMLElement, s: State, route: InboxRoute, actions: PhoneActions): void {
  const x = route.session ? s.sessions[route.session] : undefined
  // One item's view is rebuilt only for news of its own session, so a
  // half-filled question survives other sessions' news; the count on its
  // way back is kept up to date in place.
  const permission = notifySupport() === 'ok' ? Notification.permission : ''
  const deps = route.item
    ? ['item', route, x, x ? s.repos[x.repoId] : undefined, s.approvers, s.me, s.world, s.link, !!s.loading, inputsVersion()]
    : ['list', route, s.sessions, s.repos, s.approvers, s.me, s.link, !!s.loading, s.world, s.notify, s.push, s.pushError, permission]
  const back = root.querySelector<HTMLElement>('.phone-back')
  if (back) back.textContent = backText(s)
  if (lastDeps && lastDeps.length === deps.length && lastDeps.every((d, i) => d === deps[i])) return
  const moved = !lastDeps || lastDeps[1] !== route
  lastDeps = deps
  root.textContent = ''
  if (s.approvers === null) {
    root.appendChild(el('p', { class: 'phone-empty', text: 'Connecting to the citadel…' }))
    return
  }
  root.append(...(route.item ? item(s, route, actions) : list(s, route, actions)))
  if (moved) root.scrollTop = 0
}
