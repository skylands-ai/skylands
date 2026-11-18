import * as THREE from 'three'
import { describe, expect, it } from 'vitest'
import type { Repo } from '../protocol'
import { layoutIsland, MAX_BUILDINGS, placeTerrace, ropeColor, terraceLots } from './terrace'
import type { TerracePlot, WorldTheme } from './theme'

function repo(id: string, files: number): Repo {
  return {
    host: 'secret-host',
    id,
    name: `secret-name-${id}`,
    path: `/home/secret/${id}`,
    totalFiles: files,
    files: Array.from({ length: files }, (_, i) => ({ path: `secret/dir/f${i}.ts`, size: 100 + i * 37 })),
    rules: [],
  } as unknown as Repo
}

/** A theme that draws empty meshes and keeps everything it is given. */
function recordingTheme(): { theme: WorldTheme; given: unknown[] } {
  const given: unknown[] = []
  const mat = () => new THREE.MeshStandardMaterial()
  const theme = {
    buildings: { rest: mat(), read: mat(), edit: mat(), conflict: mat(), selected: mat() },
    buildTerrace(plot: TerracePlot) {
      given.push(plot)
      return { group: new THREE.Group(), animate() {} }
    },
    building(height: number) {
      given.push(height)
      return new THREE.Mesh()
    },
  } as unknown as WorldTheme
  return { theme, given }
}

describe('terraceLots', () => {
  it('gives every file a lot of its own on the plaza, whatever shape the grid takes', () => {
    for (let n = 1; n <= 64; n++) {
      // One terrace alone, and three sharing a small island, which grows to hold them.
      const alone = layoutIsland(9, [repo('r', n)]).terraces
      expect(alone[0].files).toHaveLength(n)
      const shared = layoutIsland(6.5, [repo('a', n), repo('b', n), repo('c', n)]).terraces
      for (const t of shared) expect(t.files).toHaveLength(n)
      for (const spec of [...alone, ...shared]) {
        const lots = terraceLots(spec, 3)
        expect(lots).toHaveLength(spec.files.length)
        expect(new Set(lots.map((l) => `${l.x},${l.z}`)).size).toBe(lots.length)
        for (const l of lots) {
          expect(Math.abs(l.x)).toBeLessThan(spec.sx / 2)
          expect(Math.abs(l.z)).toBeLessThan(spec.sz / 2)
        }
      }
    }
  })
})

describe('layoutIsland', () => {
  /** The corners of a terrace's plaza, with room for its posts, from the island's middle. */
  const reach = (t: { x: number; z: number; sx: number; sz: number }) => Math.hypot(Math.abs(t.x) + t.sx / 2 + 0.3, Math.abs(t.z) + t.sz / 2 + 0.3)

  it('grows the island to hold every terrace whole, apart from the others and inside its edge', () => {
    for (let n = 1; n <= 12; n++) {
      for (const files of [1, 7, 64]) {
        const repos = Array.from({ length: n }, (_, i) => repo(`r${i}`, files))
        const { R, terraces } = layoutIsland(6.5, repos)
        expect(terraces.map((t) => t.id)).toEqual(repos.map((r) => r.id))
        expect(R).toBeGreaterThanOrEqual(6.5)
        for (const t of terraces) {
          expect(t.files).toHaveLength(files)
          // Inside the dips of the island's wobbly edge.
          expect(reach(t)).toBeLessThanOrEqual(R * 0.8 + 1e-9)
        }
        for (let i = 0; i < n; i++) {
          for (let j = i + 1; j < n; j++) {
            const a = terraces[i]
            const b = terraces[j]
            const apart = Math.abs(a.x - b.x) >= (a.sx + b.sx) / 2 + 0.6 || Math.abs(a.z - b.z) >= (a.sz + b.sz) / 2 + 0.6
            expect(apart, `${n} terraces of ${files}: ${a.id} and ${b.id}`).toBe(true)
          }
        }
      }
    }
  })

  it('sizes an island by how many terraces it holds, not by how many files they show', () => {
    for (let n = 1; n <= 9; n++) {
      const of = (files: number) => layoutIsland(6.5, Array.from({ length: n }, (_, i) => repo(`r${i}`, files)))
      const full = of(MAX_BUILDINGS)
      for (const files of [0, 1, 9, 30]) {
        const small = of(files)
        expect(small.R).toBe(full.R)
        expect(small.terraces.map((t) => [t.x, t.z])).toEqual(full.terraces.map((t) => [t.x, t.z]))
      }
    }
    const mixed = layoutIsland(6.5, [repo('a', 1), repo('b', 64)])
    expect(mixed.R).toBe(layoutIsland(6.5, [repo('a', 64), repo('b', 64)]).R)
  })

  it('keeps an island its own size while its terraces fit, and grows it with each row past that', () => {
    expect(layoutIsland(9, [repo('r', 64)]).R).toBe(9)
    expect(layoutIsland(9, []).R).toBe(9)
    const size = (n: number) => layoutIsland(6.5, Array.from({ length: n }, (_, i) => repo(`r${i}`, 3))).R
    const sizes = [1, 2, 4, 6, 9].map(size)
    for (let i = 1; i < sizes.length; i++) expect(sizes[i]).toBeGreaterThan(sizes[i - 1])
    // Three fill the rows four do.
    expect(size(3)).toBe(size(4))
    // A small island with one full terrace gives all 64 files a building, rather than fewer.
    const [one] = layoutIsland(6.5, [repo('r', 64)]).terraces
    expect([one.cols, one.rows, one.files.length]).toEqual([8, 8, 64])
  })

  it('puts one terrace in the middle, and two side by side', () => {
    const [one] = layoutIsland(8, [repo('a', 9)]).terraces
    expect([one.x, one.z]).toEqual([0, 0])
    const [a, b] = layoutIsland(8, [repo('a', 64), repo('b', 64)]).terraces
    expect(a.z).toBe(b.z)
    expect(a.x).toBe(-b.x)
  })
})

describe('placeTerrace', () => {
  it('gives the theme nothing that names the host, the repo, or its files', () => {
    const [spec] = layoutIsland(8, [repo('secret-repo', 4)]).terraces
    const { theme, given } = recordingTheme()
    placeTerrace(theme, spec, 7)
    expect(JSON.stringify(given)).not.toContain('secret')
    expect(given[0]).toEqual({ sx: spec.sx, sz: spec.sz, color: ropeColor(spec.id) })
    expect(given.slice(1)).toHaveLength(4)
    expect(given.slice(1).every((h) => typeof h === 'number')).toBe(true)
  })

  it('places the terrace, and stands a building per file on its lot with what a click on it means', () => {
    const spec = layoutIsland(8, [repo('r1', 3), repo('r2', 16)]).terraces[1]
    const { theme } = recordingTheme()
    const { meshes, blds } = placeTerrace(theme, spec, 7)
    expect(meshes.group.position.toArray()).toEqual([spec.x, 0, spec.z])
    expect(meshes.group.userData.terraceId).toBe('r2')
    expect(blds.map((b) => b.userData.file)).toEqual(spec.files.map((f) => f.path))
    blds.forEach((b, i) => {
      expect(b.parent).toBe(meshes.group)
      expect(b.userData).toMatchObject({ terraceId: 'r2', bldIdx: i })
      expect(b.material).toBe(theme.buildings.rest)
      // Lots are on the plaza, from its center.
      expect(Math.abs(b.position.x)).toBeLessThan(spec.sx / 2)
      expect(Math.abs(b.position.z)).toBeLessThan(spec.sz / 2)
    })
  })
})
