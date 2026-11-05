import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'
import type { Permission } from './protocol'
import { api } from './signin'

const utf8 = new TextEncoder()

/**
 * The SHA-256 of text's UTF-8 bytes, in lowercase hex, as the daemon hashes
 * a tool call's input. It runs in plain JavaScript, so it works where the
 * browser offers no WebCrypto, as on a citadel reached over plain HTTP.
 */
export function hashText(text: string): string {
  return bytesToHex(sha256(utf8.encode(text)))
}

/** A waiting request's input, as far as this page has it. */
export interface InputView {
  /** The whole input, once this page has it and its hash matches the request's. */
  text: string | null
  /** The input as JSON, when it is JSON. */
  parsed: unknown
  /** The hash of text, which an approval names; null until text is known. */
  hash: string | null
  /** The start of the input, to show while the rest loads. */
  preview: string
  loading: boolean
  error: string | null
}

interface Fetched {
  text?: string
  error?: string
}

/** Whole inputs fetched from hosts, by host, session, and request. */
const fetched = new Map<string, Fetched>()
const asked = new Set<string>()
let version = 0

/** Changes whenever a fetched input arrives, so a view that shows inputs knows to redraw. */
export function inputsVersion(): number {
  return version
}

function parse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

/**
 * The input of a waiting request. A preview that is the whole input is
 * used as it is; otherwise the whole input is fetched from the host, once,
 * and changed is called when it arrives. Either way the text is used only
 * if its hash is the one the agent's request was recorded with.
 */
export function requestInput(host: string, sessionId: string, p: Permission, changed: () => void): InputView {
  const preview = p.inputPreview ?? ''
  const verified = (text: string): InputView =>
    hashText(text) === p.inputHash
      ? { text, parsed: parse(text), hash: p.inputHash, preview, loading: false, error: null }
      : { text: null, parsed: undefined, hash: null, preview, loading: false, error: 'The input this page got isn’t the one the agent asked about, so it can’t be approved here. Reload, or answer on the host.' }
  if (utf8.encode(preview).length === p.inputSize) return verified(preview)

  const key = `${host}/${sessionId}/${p.requestId}`
  const got = fetched.get(key)
  if (got?.text !== undefined) return verified(got.text)
  if (got?.error) return { text: null, parsed: undefined, hash: null, preview, loading: false, error: got.error }
  if (!asked.has(key)) {
    asked.add(key)
    void (async () => {
      try {
        const resp = await api(`/api/sessions/${encodeURIComponent(host)}/${encodeURIComponent(sessionId)}/requests/${encodeURIComponent(p.requestId)}/input`)
        const body = (await resp.json().catch(() => ({}))) as { input?: string; error?: string }
        fetched.set(key, resp.ok && typeof body.input === 'string' ? { text: body.input } : { error: body.error ?? `The host answered ${resp.status}.` })
      } catch (err) {
        fetched.set(key, { error: `Couldn’t get the input from the host: ${(err as Error).message}` })
      }
      version++
      changed()
    })()
  }
  return { text: null, parsed: undefined, hash: null, preview, loading: true, error: null }
}
