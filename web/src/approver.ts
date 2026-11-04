import { createOperator, createUser, fromSeed, type KeyPair } from '@nats-io/nkeys'

/** Something signed with an nkey, as internal/approval verifies it: the exact JSON that was signed, the signer's public key, and the signature in unpadded base64url. */
export interface Signed {
  payload: string
  signer: string
  sig: string
}

const utf8 = new TextEncoder()

/** Signs payload as JSON with kp. The JSON is kept as a string, so what is verified is byte for byte what was signed. */
export function sign(kp: KeyPair, payload: unknown): Signed {
  const json = JSON.stringify(payload)
  return { payload: json, signer: kp.getPublicKey(), sig: toBase64url(kp.sign(utf8.encode(json))) }
}

export function newOwnerKey(): KeyPair {
  return createOperator()
}

export function newApproverKey(): KeyPair {
  return createUser()
}

/** A key pair's seed as text, such as "SO…" for an owner key. */
export function seedOf(kp: KeyPair): string {
  return new TextDecoder().decode(kp.getSeed())
}

export function keyFromSeed(seed: string): KeyPair {
  return fromSeed(utf8.encode(seed.trim()))
}

/** A name for this browser, such as "Chrome on Linux", which a person can change. */
export function browserName(): string {
  const ua = navigator.userAgent
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : 'this device'
  return `${browser} on ${os}`
}

/** This browser's approver key, kept for the citadel's origin. */
export interface StoredApprover {
  seed: string
  key: string
  name: string
  /** The owner key of the world it approves for. */
  world: string
  created: string
}

const DB = 'skylands'
const STORE = 'approver'
const SELF = 'self'

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1)
    req.onupgradeneeded = () => req.result.createObjectStore(STORE)
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error ?? new Error('IndexedDB is unavailable'))
  })
}

/** This browser's approver key, or null if it has none. */
export async function loadApprover(): Promise<StoredApprover | null> {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const req = db.transaction(STORE, 'readonly').objectStore(STORE).get(SELF)
    req.onsuccess = () => resolve((req.result as StoredApprover | undefined) ?? null)
    req.onerror = () => reject(req.error ?? new Error('read the approver key'))
  })
}

export async function saveApprover(a: StoredApprover): Promise<void> {
  const db = await openDB()
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite')
    tx.objectStore(STORE).put(a, SELF)
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error ?? new Error('save the approver key'))
  })
}

/** Asks the browser to keep this origin's storage, and says whether it will. */
export async function keepStorage(): Promise<boolean> {
  try {
    if (await navigator.storage?.persisted?.()) return true
    return (await navigator.storage?.persist?.()) ?? false
  } catch {
    return false
  }
}

/** What the recovery file holds: the world's owner key, in the clear or encrypted with a passphrase. */
export interface RecoveryFile {
  skylands: 'owner-recovery'
  version: 1
  world: string
  owner: string
  seed?: string
  encrypted?: { kdf: 'PBKDF2-SHA256'; iterations: number; salt: string; iv: string; cipher: 'AES-GCM'; data: string }
}

/** Whether this page can encrypt a recovery file: WebCrypto needs a secure context, such as HTTPS or 127.0.0.1. */
export function canEncrypt(): boolean {
  return !!globalThis.crypto?.subtle
}

const ITERATIONS = 600_000

async function passKey(passphrase: string, salt: Uint8Array<ArrayBuffer>, iterations: number): Promise<CryptoKey> {
  const base = await crypto.subtle.importKey('raw', utf8.encode(passphrase), 'PBKDF2', false, ['deriveKey'])
  return crypto.subtle.deriveKey({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations }, base, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
}

/** The recovery file for a world's owner key, encrypted when a passphrase is given. */
export async function recoveryFile(world: string, owner: KeyPair, passphrase: string): Promise<RecoveryFile> {
  const file: RecoveryFile = { skylands: 'owner-recovery', version: 1, world, owner: owner.getPublicKey() }
  if (!passphrase) return { ...file, seed: seedOf(owner) }
  const salt = crypto.getRandomValues(new Uint8Array(16))
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const data = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, await passKey(passphrase, salt, ITERATIONS), utf8.encode(seedOf(owner))))
  return { ...file, encrypted: { kdf: 'PBKDF2-SHA256', iterations: ITERATIONS, salt: toBase64url(salt), iv: toBase64url(iv), cipher: 'AES-GCM', data: toBase64url(data) } }
}

/** The owner key a recovery file holds, decrypted with the passphrase if it is encrypted. */
export async function ownerFromRecovery(file: RecoveryFile, passphrase: string): Promise<KeyPair> {
  if (file.skylands !== 'owner-recovery') throw new Error('This is not a Skylands recovery file.')
  let seed = file.seed
  if (file.encrypted) {
    if (!canEncrypt()) throw new Error('This file is encrypted, and this page can only decrypt it over HTTPS or at 127.0.0.1.')
    const e = file.encrypted
    try {
      const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: fromBase64url(e.iv) }, await passKey(passphrase, fromBase64url(e.salt), e.iterations), fromBase64url(e.data))
      seed = new TextDecoder().decode(plain)
    } catch {
      throw new Error('That passphrase doesn’t open this file.')
    }
  }
  if (!seed) throw new Error('This recovery file holds no key.')
  const kp = keyFromSeed(seed)
  if (kp.getPublicKey() !== file.owner) throw new Error('This recovery file is damaged: its key doesn’t match the owner it names.')
  return kp
}

export function toBase64url(b: Uint8Array): string {
  let s = ''
  for (const x of b) s += String.fromCharCode(x)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fromBase64url(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}
