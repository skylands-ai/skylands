/**
 * Installing the citadel as an app, where the browser offers it: Chrome,
 * Edge, and other Chromium browsers, whose installed apps share the
 * browser's storage and so its approver key. Safari never offers it, and
 * isn't supported: its installed apps keep storage apart from Safari's, so
 * they would start without the key.
 */

/** Chrome's offer to install, which the page holds until a person asks. */
interface InstallOffer extends Event {
  prompt(): Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

/** Where this page stands: running as the installed app, able to be installed, just installed, or none of these. */
export type InstallState = 'app' | 'offered' | 'installed' | 'no'

let offer: InstallOffer | null = null

/**
 * Registers the service worker, which Chrome wants before it offers to
 * install, and follows the offer, telling onChange where the page stands.
 * A page without a secure context gets neither.
 */
export function watchInstall(onChange: (state: InstallState) => void): void {
  if (!window.isSecureContext || !('serviceWorker' in navigator)) return
  navigator.serviceWorker.register('/sw.js').catch((err: unknown) => console.warn('service worker:', err))
  if (matchMedia('(display-mode: standalone)').matches) onChange('app')
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    offer = e as InstallOffer
    onChange('offered')
  })
  window.addEventListener('appinstalled', () => {
    offer = null
    onChange('installed')
  })
}

/** Shows the browser's install dialog, and resolves with whether the app was installed. */
export async function installApp(): Promise<boolean> {
  const o = offer
  if (!o) return false
  // An offer can be shown once. The browser makes another if it's declined.
  offer = null
  await o.prompt()
  return (await o.userChoice).outcome === 'accepted'
}
