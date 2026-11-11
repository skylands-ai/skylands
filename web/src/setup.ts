import '@fontsource/barlow/400.css'
import '@fontsource/barlow/600.css'
import '@fontsource/barlow/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/sora/700.css'
import './style.css'
import type { KeyPair } from '@nats-io/nkeys'
import {
  canEncrypt,
  browserName,
  keepStorage,
  keyFromSeed,
  loadApprover,
  newApproverKey,
  newOwnerKey,
  ownerFromRecovery,
  recoveryFile,
  saveApprover,
  seedOf,
  sign,
  type RecoveryFile,
  type Signed,
} from './approver'
import { byId, el } from './dom'
import { signIn } from './signin'
import type { ApproverInfo, VoucherInfo, WorldInfo } from './protocol'

/**
 * The setup page. Until a world exists, whoever opens the link the citadel
 * printed sets it up here: the owner key is made in this browser and leaves
 * it only inside the recovery file the person downloads, and this browser
 * becomes the world's first approver. Once the world exists, a browser
 * that isn't an approver becomes one here with an "Add an approver" link
 * from an approver, or with the recovery file.
 */

const root = byId<HTMLElement>('setup')
/**
 * The link's fragment, which the browser never sends to the server by
 * itself: the setup token, or "approver=" and the claim seed of an "Add an
 * approver" link.
 */
const token = (): string => (claimSeed() ? '' : location.hash.replace(/^#/, ''))
const claimSeed = (): string | null => /^#approver=(.+)$/.exec(location.hash)?.[1] ?? null

/** Every approver key the world's registry knows, or none if the citadel can't say yet. */
async function getApprovers(): Promise<ApproverInfo[]> {
  const resp = await fetch('/api/world/approvers').catch(() => null)
  if (!resp?.ok) return []
  return ((await resp.json()) as { approvers?: ApproverInfo[] }).approvers ?? []
}

/** name, or name with a number added if an approver already has it, so every approver's name is its own. */
function uniqueName(name: string, approvers: ApproverInfo[]): string {
  const taken = new Set(approvers.filter((a) => a.valid).map((a) => a.name))
  if (!taken.has(name)) return name
  for (let n = 2; ; n++) if (!taken.has(`${name} ${n}`)) return `${name} ${n}`
}

const timeOf = (iso: string): string => new Date(iso).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })

// version is the citadel's skylands version, from its world's description.
let version: string | undefined

function show(...kids: (Node | string | null)[]): void {
  const foot = version ? el('p', { class: 'version', title: `This citadel runs skylands ${version}`, text: `Skylands ${version}` }) : null
  root.replaceChildren(...[...kids, foot].filter((k): k is Node | string => k !== null))
}

async function getWorld(): Promise<WorldInfo> {
  const resp = await fetch('/api/world')
  if (!resp.ok) throw new Error(`The citadel answered ${resp.status}.`)
  const w = (await resp.json()) as WorldInfo
  version = w.version
  return w
}

async function post(path: string, body: unknown): Promise<{ ok: boolean; status: number; error: string }> {
  const resp = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const data = (await resp.json().catch(() => ({}))) as { error?: string }
  return { ok: resp.ok, status: resp.status, error: data.error ?? `The citadel answered ${resp.status}.` }
}

function problems(w: WorldInfo): Node | null {
  if (!w.problems?.length) return null
  return el('div', { class: 'problem' }, [
    el('b', { text: 'NATS is missing settings this world needs.' }),
    el('ul', {}, w.problems.map((p) => el('li', { text: p }))),
    w.natsSettingsUrl ? el('a', { href: w.natsSettingsUrl, target: '_blank', rel: 'noopener', text: 'The NATS settings Skylands needs' }) : null,
  ])
}

function errorBox(text: string): HTMLElement {
  return el('div', { class: 'error', role: 'alert', text })
}

const privateNote = (): HTMLElement =>
  el('p', { class: 'hint', text: 'Don’t use a private window: it forgets this browser’s key when it closes, and you would have to enroll it again.' })

async function render(): Promise<void> {
  let w: WorldInfo
  try {
    w = await getWorld()
  } catch (err) {
    show(el('h1', { text: 'Skylands' }), errorBox(`Can’t reach the citadel: ${(err as Error).message}`))
    setTimeout(() => void render(), 3000)
    return
  }
  if (!w.checked) {
    show(el('h1', { text: 'Skylands' }), el('p', { text: 'Waiting for the citadel to reach its NATS server, so it can tell whether this world is set up…' }), problems(w))
    setTimeout(() => void render(), 2000)
    return
  }
  if (w.ready) {
    const seed = claimSeed()
    return seed ? renderClaim(w, seed) : renderReady(w)
  }
  if (!token()) {
    show(
      el('h1', { text: 'Set up this world' }),
      el('p', { text: 'This world isn’t set up yet. Open the setup link the citadel printed when it started; it ends in /setup# and a code. It’s also shown by “skylands daemon status” on a devworld.' }),
      problems(w),
    )
    return
  }
  renderCreate(w)
}

/** The form that creates the world. */
function renderCreate(w: WorldInfo): void {
  const name = el('input', { type: 'text', id: 'worldName', value: 'Skylands', maxlength: '80', autocomplete: 'off' })
  const nameInput = el('input', { type: 'text', id: 'browserName', value: browserName(), maxlength: '80', autocomplete: 'off' })
  const pass = el('input', { type: 'password', id: 'passphrase', autocomplete: 'new-password' })
  const error = el('div')
  const create = el('button', { type: 'button', class: 'btn primary', text: 'Create the world' })
  create.addEventListener('click', () => {
    const worldName = name.value.trim()
    const label = nameInput.value.trim() || browserName()
    if (!worldName) {
      error.replaceChildren(errorBox('Give the world a name.'))
      return
    }
    void prepare(worldName, label, canEncrypt() ? pass.value : '')
  })
  show(
    el('h1', { text: 'Set up this world' }),
    el('p', { text: 'You’ll own this world. Its owner key is made here, in this browser, and only you keep it, in a recovery file you download next. This browser becomes the world’s first approver, which can approve what agents do.' }),
    problems(w),
    el('label', { for: 'worldName', text: 'World name' }),
    name,
    el('label', { for: 'browserName', text: 'Name for this browser' }),
    nameInput,
    canEncrypt()
      ? el('div', {}, [
          el('label', { for: 'passphrase', text: 'Passphrase for the recovery file (optional)' }),
          pass,
          el('p', { class: 'hint', text: 'Encrypts the recovery file. Without it, keep the file somewhere private, such as a password manager.' }),
        ])
      : el('p', { class: 'hint', text: 'This page can’t encrypt the recovery file here, because the browser only allows that over HTTPS or at 127.0.0.1. Keep the file somewhere private, such as a password manager.' }),
    privateNote(),
    error,
    el('div', { class: 'actions' }, [create]),
  )
}

/** Makes the keys and the recovery file, then asks the person to save it before anything is sent. */
async function prepare(worldName: string, label: string, passphrase: string): Promise<void> {
  let owner: KeyPair | null = newOwnerKey()
  const approver = newApproverKey()
  const created = new Date().toISOString().replace(/\.\d+Z$/, 'Z')
  const ownerPub = owner.getPublicKey()
  const world: Signed = sign(owner, { type: 'world', name: worldName, owner: ownerPub, created })
  const entry: Signed = sign(owner, { type: 'approver', world: ownerPub, key: approver.getPublicKey(), name: label, created })
  const file = await recoveryFile(worldName, owner, passphrase)
  // From here the owner key exists only in the recovery file.
  owner.clear()
  owner = null

  const fileName = `skylands-${worldName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'world'}-recovery.json`
  const href = URL.createObjectURL(new Blob([JSON.stringify(file, null, 2) + '\n'], { type: 'application/json' }))
  const download = el('a', { class: 'btn', href, download: fileName, text: 'Download the recovery file' })
  const saved = el('input', { type: 'checkbox', id: 'saved' })
  const finish = el('button', { type: 'button', class: 'btn primary', text: 'Finish setting up', disabled: '' })
  const error = el('div')
  saved.addEventListener('change', () => {
    finish.disabled = !saved.checked
  })
  finish.addEventListener('click', () => {
    finish.disabled = true
    void (async () => {
      try {
        await saveApprover({ seed: seedOf(approver), key: approver.getPublicKey(), name: label, world: ownerPub, created })
      } catch (err) {
        error.replaceChildren(errorBox(`This browser can’t keep its key: ${(err as Error).message}`))
        finish.disabled = false
        return
      }
      const r = await post('/api/world/setup', { token: token(), world, approver: entry })
      if (!r.ok) {
        error.replaceChildren(errorBox(r.error))
        finish.disabled = r.status === 409 || r.status === 403
        return
      }
      URL.revokeObjectURL(href)
      history.replaceState(null, '', '/setup')
      await done(worldName, `${worldName} is set up, and this browser approves for it.`)
    })()
  })
  show(
    el('h1', { text: 'Save your recovery file' }),
    el('p', { text: 'It holds the world’s owner key, and it’s the only copy. With it you can make a browser an approver again if every approver for this world is lost. Nobody else, the citadel included, can do that for you.' }),
    file.encrypted ? el('p', { class: 'hint', text: 'It’s encrypted with your passphrase, which you’ll need with it.' }) : el('p', { class: 'hint', text: 'It isn’t encrypted, so anyone who has it owns this world. Keep it private.' }),
    el('div', { class: 'actions' }, [download]),
    el('label', { class: 'check' }, [saved, 'I saved the recovery file somewhere safe']),
    error,
    el('div', { class: 'actions' }, [finish]),
  )
}

/** The world exists: this browser approves for it, or can become an approver with the recovery file. */
async function renderReady(w: WorldInfo): Promise<void> {
  const [mine, approvers] = await Promise.all([loadApprover().catch(() => null), getApprovers()])
  const entry = mine && mine.world === w.owner ? approvers.find((a) => a.key === mine.key) : undefined
  if (mine && entry?.valid) {
    show(el('h1', { text: w.name ?? 'Skylands' }), el('p', { text: `This world is set up, and this browser, “${mine.name}”, approves for it.` }), el('div', { class: 'actions' }, [el('a', { class: 'btn primary', href: '/', text: 'Open the world' })]))
    return
  }
  const why = !entry
    ? 'This world is set up, but this browser doesn’t approve for it.'
    : entry.revoked
      ? 'This browser’s key was revoked, so it no longer approves for this world.'
      : 'This browser’s key was removed along with the approver that added it, so it no longer approves for this world.'
  const fileInput = el('input', { type: 'file', id: 'recoveryFile', accept: 'application/json,.json' })
  const pass = el('input', { type: 'password', id: 'recoveryPass', autocomplete: 'current-password' })
  const nameInput = el('input', { type: 'text', id: 'browserName', value: uniqueName(browserName(), approvers), maxlength: '80', autocomplete: 'off' })
  const error = el('div')
  const recover = el('button', { type: 'button', class: 'btn primary', text: 'Enroll this browser' })
  recover.addEventListener('click', () => {
    void (async () => {
      error.replaceChildren()
      const f = fileInput.files?.[0]
      if (!f) {
        error.replaceChildren(errorBox('Choose your recovery file.'))
        return
      }
      recover.disabled = true
      try {
        const file = JSON.parse(await f.text()) as RecoveryFile
        if (file.owner !== w.owner) throw new Error('This recovery file is for another world.')
        const owner = await ownerFromRecovery(file, pass.value)
        const approver = newApproverKey()
        const created = new Date().toISOString().replace(/\.\d+Z$/, 'Z')
        const label = uniqueName(nameInput.value.trim() || browserName(), approvers)
        const entry = sign(owner, { type: 'approver', world: file.owner, key: approver.getPublicKey(), name: label, created })
        owner.clear()
        await saveApprover({ seed: seedOf(approver), key: approver.getPublicKey(), name: label, world: file.owner, created })
        const r = await post('/api/world/approvers', { approver: entry })
        if (!r.ok) throw new Error(r.error)
        await done(w.name ?? 'Skylands', `This browser, “${label}”, now approves for ${w.name ?? 'this world'}.`)
      } catch (err) {
        error.replaceChildren(errorBox(err instanceof SyntaxError ? 'That file isn’t a Skylands recovery file.' : (err as Error).message))
        recover.disabled = false
      }
    })()
  })
  show(
    el('h1', { text: w.name ?? 'Skylands' }),
    el('p', { text: `${why} To make it an approver, open an “Add an approver” link from a browser that approves for this world, or, if you own the world, use your recovery file here.` }),
    el('label', { for: 'recoveryFile', text: 'Recovery file' }),
    fileInput,
    el('label', { for: 'recoveryPass', text: 'Passphrase, if the file has one' }),
    pass,
    el('label', { for: 'browserName', text: 'Name for this browser' }),
    nameInput,
    privateNote(),
    error,
    el('div', { class: 'actions' }, [recover]),
  )
}

/** An "Add an approver" link: this browser becomes an approver with the voucher whose claim seed the link carries. */
async function renderClaim(w: WorldInfo, seed: string): Promise<void> {
  const title = el('h1', { text: w.name ?? 'Skylands' })
  let claim: KeyPair
  try {
    claim = keyFromSeed(seed)
  } catch {
    show(title, errorBox('This link is damaged. Ask the approver who made it for a new one.'))
    return
  }
  const claimPub = claim.getPublicKey()
  const [mine, approvers] = await Promise.all([loadApprover().catch(() => null), getApprovers()])
  if (mine && mine.world === w.owner && approvers.find((a) => a.key === mine.key)?.valid) {
    show(
      title,
      el('p', { text: `This browser, “${mine.name}”, already approves for this world, so it left the link unused. Open it on the browser you meant to add; it works until it expires.` }),
      el('div', { class: 'actions' }, [el('a', { class: 'btn primary', href: '/', text: 'Open the world' })]),
    )
    return
  }
  const resp = await fetch(`/api/world/vouchers/${encodeURIComponent(claimPub)}`)
  const v = (await resp.json().catch(() => ({}))) as VoucherInfo & { error?: string }
  const refuse = !resp.ok
    ? (v.error ?? `The citadel answered ${resp.status}.`)
    : v.usedBy
      ? 'This link was already used. Ask an approver for a new one.'
      : v.expired
        ? 'This link expired. Ask an approver for a new one.'
        : !v.valid
          ? 'The approver that made this link no longer approves for this world. Ask another for a new one.'
          : ''
  if (refuse) {
    show(title, errorBox(refuse))
    return
  }
  const nameInput = el('input', { type: 'text', id: 'browserName', value: uniqueName(browserName(), approvers), maxlength: '80', autocomplete: 'off' })
  const error = el('div')
  const join = el('button', { type: 'button', class: 'btn primary', text: 'Become an approver' })
  join.addEventListener('click', () => {
    join.disabled = true
    void (async () => {
      const label = uniqueName(nameInput.value.trim() || browserName(), approvers)
      const approver = newApproverKey()
      const created = new Date().toISOString().replace(/\.\d+Z$/, 'Z')
      const entry = sign(claim, { type: 'approver', world: w.owner, key: approver.getPublicKey(), name: label, created, voucher: claimPub })
      try {
        await saveApprover({ seed: seedOf(approver), key: approver.getPublicKey(), name: label, world: w.owner ?? '', created })
      } catch (err) {
        error.replaceChildren(errorBox(`This browser can’t keep its key: ${(err as Error).message}`))
        join.disabled = false
        return
      }
      const r = await post('/api/world/approvers', { approver: entry })
      if (!r.ok) {
        error.replaceChildren(errorBox(r.error))
        join.disabled = r.status === 409 || r.status === 410
        return
      }
      claim.clear()
      // The link is used up; keep its seed out of the address bar and history.
      history.replaceState(null, '', '/setup')
      await done(w.name ?? 'Skylands', `This browser, “${label}”, now approves for ${w.name ?? 'this world'}.`)
    })()
  })
  show(
    title,
    el('p', { text: `${v.byName} invited this browser to approve what agents do in ${w.name ?? 'this world'}. The link works once, until ${timeOf(v.expires)}.` }),
    el('label', { for: 'browserName', text: 'Name for this browser' }),
    nameInput,
    privateNote(),
    error,
    el('div', { class: 'actions' }, [join]),
  )
}

/**
 * The end of setup, recovery, or an invite link: signs in with the new key,
 * so the world opens signed in, and asks the browser to keep this origin's
 * storage, and says whether it will.
 */
async function done(title: string, message: string): Promise<void> {
  const [, kept] = await Promise.all([signIn(), keepStorage()])
  show(
    el('h1', { text: title }),
    el('p', { text: message }),
    el('p', {
      class: 'hint',
      text: kept
        ? 'This browser promised to keep its key.'
        : 'This browser didn’t promise to keep its key, so it may clear it if it runs short of space. Another approver, or your recovery file, brings it back.',
    }),
    el('div', { class: 'actions' }, [el('a', { class: 'btn primary', href: '/', text: 'Open the world' })]),
  )
}

// Opening the setup link in a tab already at /setup changes only the
// fragment, which doesn't reload the page.
window.addEventListener('hashchange', () => void render())
void render()
