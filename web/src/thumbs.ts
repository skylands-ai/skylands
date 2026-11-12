import { el } from './dom'
import { api } from './signin'

/**
 * The images a conversation's prompts carried. Each stays on its host,
 * which serves it through the citadel when the conversation is opened;
 * the last few fetched are kept here, so a rebuild doesn't fetch them
 * again.
 */

interface Kept {
  url?: string
  error?: string
  done: Promise<void>
}

/** How many fetched images are kept. */
const MAX_KEPT = 24

const kept = new Map<string, Kept>()

function fetchImage(host: string, session: string, hash: string): Kept {
  const key = `${host}/${session}/${hash}`
  const have = kept.get(key)
  if (have && !have.error) {
    // The most recently shown are the last to go.
    kept.delete(key)
    kept.set(key, have)
    return have
  }
  const k: Kept = { done: Promise.resolve() }
  k.done = (async () => {
    try {
      const resp = await api(`/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(session)}/attachments/${encodeURIComponent(hash)}`)
      if (!resp.ok) {
        const body = (await resp.json().catch(() => ({}))) as { error?: string }
        k.error = body.error ?? `The citadel answered ${resp.status}.`
        return
      }
      k.url = URL.createObjectURL(await resp.blob())
    } catch (err) {
      k.error = `Could not reach the citadel: ${(err as Error).message}`
    }
  })()
  kept.set(key, k)
  while (kept.size > MAX_KEPT) {
    const [oldest, gone] = kept.entries().next().value as [string, Kept]
    kept.delete(oldest)
    if (gone.url) URL.revokeObjectURL(gone.url)
  }
  return k
}

/** One image a prompt carried, as a thumbnail that opens the whole image. */
export function imageThumb(host: string, session: string, name: string, hash: string): HTMLElement {
  const k = fetchImage(host, session, hash)
  const box = el('a', { class: 'thumb', title: name, target: '_blank', rel: 'noopener' })
  const label = el('span', { class: 'name', text: name })
  const show = (): void => {
    if (k.url) {
      box.setAttribute('href', k.url)
      box.replaceChildren(el('img', { src: k.url, alt: name }), label)
    } else {
      box.replaceChildren(el('span', { class: 'placeholder', text: k.error ? 'Not available' : 'Fetching…', title: k.error ?? null }), label)
    }
  }
  show()
  if (!k.url && !k.error) void k.done.then(show)
  return box
}
