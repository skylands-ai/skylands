import { afterEach, describe, expect, it, vi } from 'vitest'
import { pushOrigin, pushSupport } from './push'
import type { State } from './store'

function state(world: Partial<NonNullable<State['world']>> | null): State {
  return { world: world ? { checked: true, ready: true, ...world } : null } as unknown as State
}

/** A browser with Web Push, at origin. */
function browser(origin: string, push = true): void {
  vi.stubGlobal('window', { isSecureContext: true, ...(push ? { PushManager: class {} } : {}) })
  vi.stubGlobal('navigator', { serviceWorker: {} })
  vi.stubGlobal('Notification', class {})
  vi.stubGlobal('location', { origin })
}

describe('pushSupport', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('offers push only at the citadel’s --url, while it sends them', () => {
    browser('https://skylands.example')
    expect(pushSupport(state({ url: 'https://skylands.example', pushKey: 'BK' }))).toBe('ok')
    expect(pushSupport(state({ url: 'https://skylands.example/', pushKey: 'BK' }))).toBe('ok')
    expect(pushSupport(state({ url: 'https://skylands.example' }))).toBe('off')
    expect(pushSupport(state(null))).toBe('off')
    browser('http://127.0.0.1:8080')
    expect(pushSupport(state({ url: 'https://skylands.example', pushKey: 'BK' }))).toBe('elsewhere')
  })

  it('says a browser without Web Push in a tab, such as an iPhone’s Safari, can’t have it', () => {
    browser('https://skylands.example', false)
    expect(pushSupport(state({ url: 'https://skylands.example', pushKey: 'BK' }))).toBe('unsupported')
  })

  it('names the origin pushes open at', () => {
    expect(pushOrigin(state({ url: 'https://skylands.example:8443/' }))).toBe('https://skylands.example:8443')
    expect(pushOrigin(state({}))).toBeNull()
  })
})
