import type * as THREE from 'three'

/** Who runs the agent. The hull's shape and color say which, in every theme. */
export type Provider = 'claude' | 'codex' | 'gemini' | 'goose' | 'local'

export const PROVIDER_COLOR: Record<Provider, number> = { claude: 0xf2995e, codex: 0x3fbf9c, gemini: 0x7c9bff, goose: 0x9c8466, local: 0xb58beb }

/** Maps an adapter name to its provider. Unknown adapters fly the local hull. */
export function providerOf(agent: string): Provider {
  if (agent.startsWith('claude')) return 'claude'
  if (agent.startsWith('codex')) return 'codex'
  if (agent.startsWith('gemini')) return 'gemini'
  if (agent.startsWith('goose')) return 'goose'
  return 'local'
}

export interface Airship {
  group: THREE.Group
  /** Every material the airship owns, so the whole ship can fade and be freed together. */
  mats: THREE.Material[]
  /** One per quarter of context used, hung under the hull. */
  bags: THREE.Mesh[]
}
