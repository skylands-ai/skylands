import '@fontsource/barlow/400.css'
import '@fontsource/barlow/600.css'
import '@fontsource/barlow/700.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/sora/700.css'
import './style.css'
import type { KeyPair } from '@nats-io/nkeys'
import {
  canEncrypt,
  deviceName,
  keepStorage,
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
import type { WorldInfo } from './protocol'

/**
 * The setup page. Until a world exists, whoever opens the link the citadel
 * printed sets it up here: the owner key is made in this browser and leaves
 * it only inside the recovery file the person downloads, and this browser
 * becomes the world's first approver. Once the world exists, a browser
 * that isn't an approver can become one with the recovery file.
 */

const root = byId<HTMLElement>('setup')
/** The setup token, from the link's fragment, which the browser never sends to the server by itself. */
const token = (): string => location.hash.replace(/^#/, '')

function show(...kids: (Node | string | null)[]): void {
  root.replaceChildren(...kids.filter((k): k is Node | string => k !== null))
}

async function getWorld(): Promise<WorldInfo> {
  const resp = await fetch('/api/world')
  if (!resp.ok) throw new Error(`The citadel answered ${resp.status}.`)
  return (await resp.json()) as WorldInfo
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
  if (w.ready) return renderReady(w)
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
  const device = el('input', { type: 'text', id: 'deviceName', value: deviceName(), maxlength: '80', autocomplete: 'off' })
  const pass = el('input', { type: 'password', id: 'passphrase', autocomplete: 'new-password' })
  const error = el('div')
  const create = el('button', { type: 'button', class: 'btn primary', text: 'Create the world' })
  create.addEventListener('click', () => {
    const worldName = name.value.trim()
    const deviceLabel = device.value.trim() || deviceName()
    if (!worldName) {
      error.replaceChildren(errorBox('Give the world a name.'))
      return
    }
    void prepare(worldName, deviceLabel, canEncrypt() ? pass.value : '')
  })
  show(
    el('h1', { text: 'Set up this world' }),
    el('p', { text: 'You’ll own this world. Its owner key is made here, in this browser, and only you keep it, in a recovery file you download next. This browser becomes the first device that can approve what agents do.' }),
    problems(w),
    el('label', { for: 'worldName', text: 'World name' }),
    name,
    el('label', { for: 'deviceName', text: 'Name for this browser' }),
    device,
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
async function prepare(worldName: string, deviceLabel: string, passphrase: string): Promise<void> {
  let owner: KeyPair | null = newOwnerKey()
  const approver = newApproverKey()
  const created = new Date().toISOString().replace(/\.\d+Z$/, 'Z')
  const ownerPub = owner.getPublicKey()
  const world: Signed = sign(owner, { type: 'world', name: worldName, owner: ownerPub, created })
  const entry: Signed = sign(owner, { type: 'approver', world: ownerPub, key: approver.getPublicKey(), name: deviceLabel, created })
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
        await saveApprover({ seed: seedOf(approver), key: approver.getPublicKey(), name: deviceLabel, world: ownerPub, created })
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
    el('p', { text: 'It holds the world’s owner key, and it’s the only copy. With it you can enroll a browser again if every device that approves for this world is lost. Nobody else, the citadel included, can do that for you.' }),
    file.encrypted ? el('p', { class: 'hint', text: 'It’s encrypted with your passphrase, which you’ll need with it.' }) : el('p', { class: 'hint', text: 'It isn’t encrypted, so anyone who has it owns this world. Keep it private.' }),
    el('div', { class: 'actions' }, [download]),
    el('label', { class: 'check' }, [saved, 'I saved the recovery file somewhere safe']),
    error,
    el('div', { class: 'actions' }, [finish]),
  )
}

/** The world exists: this browser approves for it, or can become an approver with the recovery file. */
async function renderReady(w: WorldInfo): Promise<void> {
  const mine = await loadApprover().catch(() => null)
  if (mine && mine.world === w.owner) {
    show(el('h1', { text: w.name ?? 'Skylands' }), el('p', { text: `This world is set up, and this browser, “${mine.name}”, approves for it.` }), el('div', { class: 'actions' }, [el('a', { class: 'btn primary', href: '/', text: 'Open the world' })]))
    return
  }
  const fileInput = el('input', { type: 'file', id: 'recoveryFile', accept: 'application/json,.json' })
  const pass = el('input', { type: 'password', id: 'recoveryPass', autocomplete: 'current-password' })
  const device = el('input', { type: 'text', id: 'deviceName', value: deviceName(), maxlength: '80', autocomplete: 'off' })
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
        const label = device.value.trim() || deviceName()
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
    el('p', { text: 'This world is set up, but this browser doesn’t approve for it. If you own the world, enroll this browser with your recovery file. Otherwise, open the world from a device that already approves for it.' }),
    el('label', { for: 'recoveryFile', text: 'Recovery file' }),
    fileInput,
    el('label', { for: 'recoveryPass', text: 'Passphrase, if the file has one' }),
    pass,
    el('label', { for: 'deviceName', text: 'Name for this browser' }),
    device,
    privateNote(),
    error,
    el('div', { class: 'actions' }, [recover, el('a', { class: 'btn ghost', href: '/', text: 'Open the world' })]),
  )
}

/** The end of setup or recovery: asks the browser to keep this origin's storage, and says whether it will. */
async function done(title: string, message: string): Promise<void> {
  const kept = await keepStorage()
  show(
    el('h1', { text: title }),
    el('p', { text: message }),
    el('p', {
      class: 'hint',
      text: kept
        ? 'This browser promised to keep its key.'
        : 'This browser didn’t promise to keep its key, so it may clear it if it runs short of space. Your recovery file brings it back.',
    }),
    el('div', { class: 'actions' }, [el('a', { class: 'btn primary', href: '/', text: 'Open the world' })]),
  )
}

// Opening the setup link in a tab already at /setup changes only the
// fragment, which doesn't reload the page.
window.addEventListener('hashchange', () => void render())
void render()
