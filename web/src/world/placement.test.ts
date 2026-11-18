import { describe, expect, it } from 'vitest'
import { placeIsland, ringDistances, type Placement, type Sized } from './placement'

function at(angle: number, dist: number, R: number): Placement {
  return { angle, dist, y: 8, R, depth: R + 2, seed: 1, phase: 0 }
}

const centers = (islands: Sized[], d: Map<string, number>) =>
  islands.map((i) => ({ R: i.R, x: Math.cos(i.def.angle) * (d.get(i.host) as number), z: Math.sin(i.def.angle) * (d.get(i.host) as number) }))

describe('ringDistances', () => {
  it('leaves islands where they were placed while they have room', () => {
    const islands = [
      { host: 'a', def: at(0, 35, 8), R: 8 },
      { host: 'b', def: at(Math.PI, 33, 9), R: 9 },
    ]
    const d = ringDistances(islands)
    expect([d.get('a'), d.get('b')]).toEqual([35, 33])
  })

  it('moves an island that grew out by as much, so its edge stays as far from the citadel', () => {
    const d = ringDistances([{ host: 'a', def: at(1, 35, 8), R: 15 }])
    expect(d.get('a')).toBe(42)
  })

  it('moves islands that grew apart, whatever order they come in', () => {
    const islands = [
      { host: 'b', def: at(0.6, 33, 7), R: 16 },
      { host: 'a', def: at(0, 34, 10), R: 20 },
      { host: 'c', def: at(1.2, 36, 9), R: 12 },
    ]
    const d = ringDistances(islands)
    expect(ringDistances([...islands].reverse())).toEqual(d)
    const c = centers(islands, d)
    for (let i = 0; i < c.length; i++) {
      for (let j = i + 1; j < c.length; j++) expect(Math.hypot(c[i].x - c[j].x, c[i].z - c[j].z)).toBeGreaterThanOrEqual(c[i].R + c[j].R)
    }
    for (const i of islands) expect(d.get(i.host) as number).toBeGreaterThanOrEqual(i.def.dist + i.R - i.def.R)
  })

  it('spaces islands placed on the ring', () => {
    const taken: Placement[] = []
    const islands: Sized[] = []
    for (const host of ['workstation', 'laptop', 'build-box', 'pi', 'studio', 'cloud-1']) {
      const def = placeIsland(host, taken)
      taken.push(def)
      islands.push({ host, def, R: def.R * 2 })
    }
    const c = centers(islands, ringDistances(islands))
    for (let i = 0; i < c.length; i++) {
      for (let j = i + 1; j < c.length; j++) expect(Math.hypot(c[i].x - c[j].x, c[i].z - c[j].z)).toBeGreaterThanOrEqual(c[i].R + c[j].R)
    }
  })
})
