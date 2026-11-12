import { describe, expect, it } from 'vitest'
import type { AgentAvailability, Island, Repo } from './protocol'
import { agentChoices, modeChoices, modelChoices, OTHER_MODEL, repoChoices } from './start'

function island(agents?: AgentAvailability[]): Island {
  return { host: 'workstation', reachable: true, agents } as Island
}

function repo(id: string, host: string, name: string): Repo {
  return { id, host, name, path: `/src/${name}` } as Repo
}

describe('agentChoices', () => {
  it('offers every known agent until the island has checked', () => {
    expect(agentChoices(null)).toEqual(['fake', 'claude', 'codex', 'gemini', 'goose'])
    expect(agentChoices(island())).toEqual(['fake', 'claude', 'codex', 'gemini', 'goose'])
  })

  it('offers what the island can run, known agents first in their order, then others by name', () => {
    const i = island([
      { name: 'zed', available: true },
      { name: 'gemini', available: true },
      { name: 'codex', available: false },
      { name: 'aider', available: true },
      { name: 'claude', available: true },
    ])
    expect(agentChoices(i)).toEqual(['claude', 'gemini', 'aider', 'zed'])
  })

  it('offers nothing when the island can run no agent', () => {
    expect(agentChoices(island([{ name: 'claude', available: false }]))).toEqual([])
  })
})

describe('modeChoices', () => {
  it('lists the modes the agent’s adapter lists on the island', () => {
    const modes = [{ id: 'default', name: 'Default' }, { id: 'plan', name: 'Plan' }]
    expect(modeChoices(island([{ name: 'claude', available: true, modes }]), 'claude')).toEqual(modes)
    expect(modeChoices(island([{ name: 'claude', available: true, modes }]), 'codex')).toEqual([])
    expect(modeChoices(null, 'claude')).toEqual([])
  })
})

describe('modelChoices', () => {
  it('offers the default and a typed id until the models are read', () => {
    const { options, none } = modelChoices(null, 'claude')
    expect(none).toBe(false)
    expect(options.map((o) => o.value)).toEqual(['', OTHER_MODEL])
    expect(options[0].text).toBe('Agent’s default')
  })

  it('offers the models the agent offers, after its default', () => {
    const i = island([{ name: 'claude', available: true, modelsRead: true, defaultModel: 'sonnet', models: [{ id: 'sonnet' }, { id: 'opus', name: 'Opus', description: 'The biggest' }] }])
    const { options, none } = modelChoices(i, 'claude')
    expect(none).toBe(false)
    expect(options.map((o) => [o.value, o.text, o.title])).toEqual([
      ['', 'Agent’s default (sonnet)', undefined],
      ['sonnet', 'sonnet', undefined],
      ['opus', 'opus', 'The biggest'],
      [OTHER_MODEL, 'Other model id…', undefined],
    ])
  })

  it('offers only the default for an agent with no model setting', () => {
    const { options, none } = modelChoices(island([{ name: 'goose', available: true, modelsRead: true, models: [] }]), 'goose')
    expect(none).toBe(true)
    expect(options.map((o) => o.value)).toEqual([''])
  })
})

describe('repoChoices', () => {
  it('groups the repositories by island, islands by host and repositories by name', () => {
    const repos = [repo('r1', 'workstation', 'storefront'), repo('r2', 'laptop', 'notes'), repo('r3', 'workstation', 'api'), repo('r4', 'laptop', 'dotfiles')]
    const groups = repoChoices({ repos: Object.fromEntries(repos.map((r) => [r.id, r])) })
    expect(groups.map((g) => [g.host, g.repos.map((r) => r.id)])).toEqual([
      ['laptop', ['r4', 'r2']],
      ['workstation', ['r3', 'r1']],
    ])
  })

  it('is empty before any repository is registered', () => {
    expect(repoChoices({ repos: {} })).toEqual([])
  })
})
