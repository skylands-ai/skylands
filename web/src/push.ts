import { keyFromSeed, loadApprover, sign } from './approver'
import { api } from './signin'
import type { State } from './store'

/**
 * Web Push: notifications the citadel sends, which reach this browser with
 * the world's page closed and which the service worker shows. A browser
 * subscribes once for each address it opens the citadel at, so the citadel
 * offers push only at its --url, where a push's link opens. Each
 * subscription is signed with this browser's approver key, so it belongs
 * to that approver.
 */

/**
 * Whether this page can get pushes, or why not: the browser has no Web
 * Push here, as in an iPhone's Safari; the citadel doesn't send them; or
 * this page is open at an address other than the citadel's --url.
 */
export type PushSupport = 'ok' | 'unsupported' | 'off' | 'elsewhere'

export function pushSupport(s: State): PushSupport {
  if (!window.isSecureContext || !('serviceWorker' in navigator) || !('PushManager' in window) || typeof Notification === 'undefined') return 'unsupported'
  if (!s.world?.pushKey) return 'off'
  return pushOrigin(s) === location.origin ? 'ok' : 'elsewhere'
}

/** The address the citadel sends pushes to pages at: its --url's origin. */
export function pushOrigin(s: State): string | null {
  try {
    return s.world?.url ? new URL(s.world.url).origin : null
  } catch {
    return null
  }
}

function fromBase64url(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function sameKey(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a || a.byteLength !== b.length) return false
  const x = new Uint8Array(a)
  return x.every((v, i) => v === b[i])
}

/** The service worker's registration, once it is active, which subscribing needs. */
async function registration(): Promise<ServiceWorkerRegistration> {
  await navigator.serviceWorker.register('/sw.js')
  return navigator.serviceWorker.ready
}

/**
 * Subscribes this browser to the citadel's pushes, or keeps the
 * subscription it has, and registers it with the citadel. A subscription
 * made with another world's push key is replaced.
 */
export async function subscribePush(s: State): Promise<void> {
  const key = s.world?.pushKey
  const world = s.world?.owner
  if (!key || !world) throw new Error('The citadel doesn’t send push notifications.')
  const me = await loadApprover().catch(() => null)
  if (!me || me.world !== world) throw new Error('This browser has no approver key for this world.')
  const appKey = fromBase64url(key)
  const reg = await registration()
  let sub = await reg.pushManager.getSubscription()
  if (sub && !sameKey(sub.options.applicationServerKey, appKey)) {
    await sub.unsubscribe()
    sub = null
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: appKey })
  const keys = sub.toJSON().keys ?? {}
  const subscription = sign(keyFromSeed(me.seed), {
    type: 'push',
    world,
    origin: location.origin,
    endpoint: sub.endpoint,
    p256dh: keys.p256dh ?? '',
    auth: keys.auth ?? '',
    created: new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
  })
  const resp = await api('/api/push/subscriptions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ subscription }) })
  if (!resp.ok) {
    const data = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new Error(data.error ?? `The citadel answered ${resp.status}.`)
  }
}

/** Ends this browser's subscription, at the citadel and in the browser. */
export async function unsubscribePush(): Promise<void> {
  const reg = await navigator.serviceWorker?.getRegistration()
  const sub = await reg?.pushManager.getSubscription()
  if (!sub) return
  await api('/api/push/unsubscribe', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ endpoint: sub.endpoint }) }).catch(() => null)
  await sub.unsubscribe()
}

/**
 * Closes the service worker's notifications for what no longer waits: it
 * was answered, here or anywhere else, it expired, or its session ended.
 * The citadel sends no push to close one, since a browser expects every
 * push to show a notification.
 */
export async function closeStale(waiting: Set<string>): Promise<void> {
  const reg = await navigator.serviceWorker?.getRegistration()
  for (const n of (await reg?.getNotifications()) ?? []) if (n.tag && !waiting.has(n.tag)) n.close()
}
