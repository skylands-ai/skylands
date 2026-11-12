import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'
import { el } from './dom'
import { mentionsIn, pickMention, sizeText, typingMention } from './mentions'
import type { Carried, FilesResult, PromptImage } from './protocol'
import { api } from './signin'

/**
 * What a prompt carries besides its text, while it is written: the files
 * it mentions with @, and the images pasted, dropped, or picked for it.
 * Each image is uploaded to the host the prompt goes to, which keeps it
 * under its SHA-256 until the prompt names it. The command bar, the
 * inbox's New form, and the reply box each have one.
 */

/** The largest image, the most images, and the types a prompt carries, as the daemon takes them. */
export const MAX_IMAGE = 10 * 1024 * 1024
export const MAX_IMAGES = 5
export const IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

/** An image attached to a prompt being written. */
export interface Attached {
  key: string
  name: string
  mimeType: string
  size: number
  /** The SHA-256 of its bytes as this browser hashed them, which the prompt names and its signature covers. */
  hash: string
  /** Its thumbnail, while it is attached. */
  url: string
  blob: Blob
  /** Its upload to each host, resolving with an error message, or empty once the host has it. */
  uploads: Map<string, Promise<string>>
  /** How its upload to the host it was last sent to went. */
  state: 'uploading' | 'ready' | 'failed'
  error?: string
}

/** A prompt's mentions and images while it is written, kept across the rebuilds of the box it is written in. */
export class Draft {
  /** The paths picked from the @ list, in the order they were picked. */
  picked: string[] = []
  images: Attached[] = []
  /** Why the last image couldn't be attached, until the next change. */
  problem = ''
  /** Draws the box again; the box that shows the draft sets it. */
  onChange: () => void = () => {}
  private keys = 0

  /** Attaches images to send to host, refusing what the daemon would. */
  async add(files: File[], host: string | null): Promise<void> {
    this.problem = ''
    for (const f of files) {
      if (!IMAGE_TYPES.includes(f.type)) {
        this.problem = `${f.name || 'That file'} isn’t a PNG, JPEG, GIF, or WebP image.`
        continue
      }
      if (f.size > MAX_IMAGE) {
        this.problem = `${f.name || 'That image'} is ${sizeText(f.size)}; an image can be at most ${MAX_IMAGE / 1024 / 1024} MB.`
        continue
      }
      if (this.images.length >= MAX_IMAGES) {
        this.problem = `A prompt carries at most ${MAX_IMAGES} images.`
        break
      }
      const bytes = new Uint8Array(await f.arrayBuffer())
      const hash = bytesToHex(sha256(bytes))
      if (this.images.some((i) => i.hash === hash)) continue
      const img: Attached = { key: `img${++this.keys}`, name: imageName(f.name, f.type), mimeType: f.type, size: f.size, hash, url: URL.createObjectURL(f), blob: f, uploads: new Map(), state: 'uploading' }
      this.images.push(img)
      if (host) void this.upload(img, host)
    }
    this.onChange()
  }

  remove(key: string): void {
    const img = this.images.find((i) => i.key === key)
    if (img) URL.revokeObjectURL(img.url)
    this.images = this.images.filter((i) => i.key !== key)
    this.problem = ''
    this.onChange()
  }

  /** Uploads an image to host once, and says how it went. */
  private upload(img: Attached, host: string): Promise<string> {
    let done = img.uploads.get(host)
    if (!done) {
      img.state = 'uploading'
      img.error = undefined
      done = uploadImage(host, img).then((err) => {
        if (err) img.uploads.delete(host)
        if (this.images.includes(img)) {
          img.state = err ? 'failed' : 'ready'
          img.error = err || undefined
          this.onChange()
        }
        return err
      })
      img.uploads.set(host, done)
    }
    return done
  }

  /** Sends every image to host, if it hasn't been, as an image goes to the host its prompt goes to. */
  sendTo(host: string): void {
    for (const img of this.images) void this.upload(img, host)
  }

  /**
   * What the prompt with this text carries: its mentions still in the
   * text, in its order, and its images, once host has every one of them.
   * Resolves with why not, when one couldn't be sent.
   */
  async carried(text: string, host: string): Promise<Carried | string> {
    const errs = await Promise.all(this.images.map((img) => this.upload(img, host)))
    const bad = errs.findIndex((e) => e)
    if (bad >= 0) return `${this.images[bad].name} couldn’t be sent: ${errs[bad]}`
    const images: PromptImage[] = this.images.map((i) => ({ name: i.name, mimeType: i.mimeType, hash: i.hash, size: i.size }))
    return { mentions: mentionsIn(text, this.picked), images }
  }

  /** Empties the draft once its prompt is sent. */
  clear(): void {
    for (const img of this.images) URL.revokeObjectURL(img.url)
    this.picked = []
    this.images = []
    this.problem = ''
    this.onChange()
  }
}

/** An image's name as a prompt sends it: one line, not too long, never empty. */
function imageName(name: string, type: string): string {
  const clean = name.replace(/[\p{Cc}\s]+/gu, ' ').trim().slice(0, 200)
  return clean || `image.${type.split('/')[1] ?? 'png'}`
}

/** Uploads an image to host through the citadel, and checks the host kept the bytes this browser hashed. Resolves with an error message, or empty. */
async function uploadImage(host: string, img: Attached): Promise<string> {
  try {
    const resp = await api(`/api/islands/${encodeURIComponent(host)}/images`, { method: 'POST', headers: { 'Content-Type': img.mimeType }, body: img.blob })
    const out = (await resp.json().catch(() => ({}))) as { hash?: string; error?: string }
    if (!resp.ok) return out.error ?? `The citadel answered ${resp.status}.`
    // The prompt is signed over this browser's own hash, so a host that
    // kept something else can't have it sent.
    if (out.hash !== img.hash) return `${host} kept a different image than the one attached`
    return ''
  } catch (err) {
    return `Could not reach the citadel: ${(err as Error).message}`
  }
}

/** Where a box's prompt goes, which decides what it can mention and attach. */
export interface ComposeTarget {
  /** The files fetch to search with what follows an @, or null when there is nothing to search yet. */
  files(): string | null
  /** The host the images go to, or null before one is chosen. */
  host(): string | null
  /** Why images can't be attached, or empty when they can. */
  noImages(): string
}

/** What a box gets to show its draft: the @ list, the attached images, and the button that picks images. */
export interface ComposeParts {
  /** The list of files that follows an @, to go just above the box, in a positioned parent. */
  menu: HTMLElement
  /** The attached images and any problem with one, to go under the box. */
  tray: HTMLElement
  /** Picks images; off, saying why, for an agent that takes none. */
  attach: HTMLButtonElement
  /** Brings the attach button up to date, as when another agent is chosen. */
  refresh(): void
}

/** How long typing after an @ pauses before the host is asked. */
const SEARCH_PAUSE_MS = 120

let menus = 0

/**
 * Lets a box mention files and attach images: typing @ lists the files the
 * target's host has, filtered as you type, and picking one puts @ and its
 * path in the text; pasting, dropping, or picking an image attaches it.
 * Its keydown listener must come before the box's own, so a key the list
 * uses doesn't also send the prompt.
 */
export function compose(input: HTMLInputElement | HTMLTextAreaElement, draft: Draft, target: ComposeTarget): ComposeParts {
  const id = `mentions-${++menus}`
  const menu = el('div', { class: 'cmd-menu mention-menu', id, role: 'listbox', 'aria-label': 'Files to mention' })
  menu.hidden = true
  const tray = el('div', { class: 'attach-tray' })
  const picker = el('input', { type: 'file', accept: IMAGE_TYPES.join(','), multiple: '', hidden: '' }) as HTMLInputElement
  const attach = el('button', { type: 'button', class: 'btn small ghost attach', text: 'Image', 'aria-label': 'Attach an image' }) as HTMLButtonElement
  attach.appendChild(picker)

  let shown: string[] = []
  let active = 0
  let typed: { at: number; query: string } | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let asked = 0

  const close = (): void => {
    menu.hidden = true
    shown = []
    typed = null
    input.removeAttribute('aria-activedescendant')
  }
  const highlight = (): void => {
    menu.querySelectorAll('[role="option"]').forEach((n, i) => n.setAttribute('aria-selected', i === active ? 'true' : 'false'))
    if (shown.length) {
      input.setAttribute('aria-activedescendant', `${id}-${active}`)
      document.getElementById(`${id}-${active}`)?.scrollIntoView({ block: 'nearest' })
    } else input.removeAttribute('aria-activedescendant')
  }
  const say = (text: string): void => {
    shown = []
    menu.replaceChildren(el('div', { class: 'cmd-none', text }))
    menu.hidden = false
    input.removeAttribute('aria-activedescendant')
  }
  const pick = (path: string): void => {
    if (!typed) return
    const caret = input.selectionStart ?? input.value.length
    const next = pickMention(input.value, typed.at, caret, path)
    input.value = next.value
    if (!draft.picked.includes(path)) draft.picked.push(path)
    close()
    input.focus()
    input.setSelectionRange(next.caret, next.caret)
    // The box keeps its own copy of what was typed, such as a draft.
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  const search = async (query: string): Promise<void> => {
    const path = target.files()
    if (!path) {
      say('Choose where the prompt goes to mention its files.')
      return
    }
    const n = ++asked
    let got: FilesResult | string
    try {
      const resp = await api(`${path}?q=${encodeURIComponent(query)}`)
      const body = (await resp.json().catch(() => ({}))) as FilesResult & { error?: string }
      got = resp.ok ? body : (body.error ?? `The citadel answered ${resp.status}.`)
    } catch (err) {
      got = `Could not reach the citadel: ${(err as Error).message}`
    }
    // Something else may have been typed while the host answered.
    if (n !== asked || !typed || typed.query !== query) return
    if (typeof got === 'string') {
      say(`The files can’t be listed: ${got}`)
      return
    }
    shown = got.files ?? []
    active = 0
    if (!shown.length) {
      say(query ? `No file matches ${query}.` : 'The repository has no files.')
      return
    }
    menu.replaceChildren(
      ...shown.map((f, i) => {
        const slash = f.lastIndexOf('/')
        return el('div', { class: 'cmd', id: `${id}-${i}`, role: 'option', 'aria-selected': 'false', title: f, onmousedown: (e) => { e.preventDefault(); pick(f) } }, [
          el('strong', { text: f.slice(slash + 1) }),
          slash > 0 ? el('span', { class: 'muted', text: f.slice(0, slash) }) : null,
        ])
      }),
      ...(got.more ? [el('div', { class: 'cmd-none', text: 'More files match; keep typing to narrow them.' })] : []),
    )
    menu.hidden = false
    highlight()
  }
  const follow = (): void => {
    const now = input.disabled ? null : typingMention(input.value, input.selectionStart ?? input.value.length)
    if (!now) {
      clearTimeout(timer)
      close()
      return
    }
    const changed = !typed || typed.at !== now.at || typed.query !== now.query
    typed = now
    if (!changed && !menu.hidden) return
    clearTimeout(timer)
    timer = setTimeout(() => void search(now.query), SEARCH_PAUSE_MS)
  }
  input.setAttribute('aria-autocomplete', 'list')
  input.addEventListener('input', follow)
  input.addEventListener('click', follow)
  input.addEventListener('blur', () => {
    clearTimeout(timer)
    close()
  })
  input.addEventListener('keydown', (e) => {
    const k = e as KeyboardEvent
    if (menu.hidden) return
    if (k.key === 'Escape') {
      k.preventDefault()
      k.stopImmediatePropagation()
      close()
      return
    }
    if (!shown.length) return
    if (k.key === 'ArrowDown' || k.key === 'ArrowUp') {
      k.preventDefault()
      k.stopImmediatePropagation()
      active = (active + (k.key === 'ArrowDown' ? 1 : shown.length - 1)) % shown.length
      highlight()
    } else if ((k.key === 'Enter' && !k.metaKey && !k.ctrlKey) || k.key === 'Tab') {
      k.preventDefault()
      k.stopImmediatePropagation()
      pick(shown[active])
    }
  })

  // Images come pasted, dropped, or picked, and only for an agent that
  // takes them.
  const take = (files: File[]): void => {
    const why = target.noImages()
    if (why) {
      draft.problem = why
      draft.onChange()
      return
    }
    void draft.add(files, target.host())
  }
  input.addEventListener('paste', (e) => {
    const data = (e as ClipboardEvent).clipboardData
    const files = [...(data?.files ?? [])].filter((f) => f.type.startsWith('image/'))
    if (!files.length) return
    // A screenshot comes as an image alone; an image copied from a page
    // may come with text, which pastes as well.
    if (!data?.getData('text/plain')) e.preventDefault()
    take(files)
  })
  input.addEventListener('dragover', (e) => {
    if ([...((e as DragEvent).dataTransfer?.items ?? [])].some((i) => i.kind === 'file')) e.preventDefault()
  })
  input.addEventListener('drop', (e) => {
    const files = [...((e as DragEvent).dataTransfer?.files ?? [])]
    if (!files.length) return
    e.preventDefault()
    take(files)
  })
  attach.addEventListener('click', (e) => {
    if (e.target !== picker) picker.click()
  })
  picker.addEventListener('change', () => {
    const files = [...(picker.files ?? [])]
    picker.value = ''
    if (files.length) take(files)
  })

  const drawTray = (): void => {
    tray.replaceChildren(
      ...draft.images.map((img) =>
        el('span', { class: `attached ${img.state}`, title: img.error ? `${img.name}: ${img.error}` : `${img.name}, ${sizeText(img.size)}` }, [
          el('img', { src: img.url, alt: '' }),
          el('span', { class: 'name', text: img.name }),
          el('span', { class: 'muted', text: img.state === 'uploading' ? 'sending…' : img.state === 'failed' ? 'not sent' : sizeText(img.size) }),
          el('button', { type: 'button', class: 'remove', 'aria-label': `Remove ${img.name}`, text: '×', onclick: () => draft.remove(img.key) }),
        ]),
      ),
      ...(draft.problem ? [el('span', { class: 'attach-problem', role: 'status', text: draft.problem })] : []),
    )
    tray.hidden = !draft.images.length && !draft.problem
  }
  const refresh = (): void => {
    const why = target.noImages()
    attach.disabled = !!why || input.disabled
    attach.title = why || (draft.images.length >= MAX_IMAGES ? `A prompt carries at most ${MAX_IMAGES} images` : 'Attach an image, or paste or drop one in the box')
  }
  draft.onChange = () => {
    drawTray()
    refresh()
  }
  drawTray()
  refresh()
  return { menu, tray, attach, refresh }
}
