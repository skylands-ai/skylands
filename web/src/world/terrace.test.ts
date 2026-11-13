import * as THREE from 'three'
import { describe, expect, it } from 'vitest'
import type { Repo } from '../protocol'
import { layoutTerraces, placeTerrace, ropeColor, terraceLots } from './terrace'
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
      // One terrace with room for all 64, and three sharing a small island, which caps how many files get buildings.
      const alone = layoutTerraces(9, [repo('r', n)])
      expect(alone[0].files).toHaveLength(n)
      for (const spec of [...alone, ...layoutTerraces(6.5, [repo('a', n), repo('b', n), repo('c', n)])]) {
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

describe('placeTerrace', () => {
  it('gives the theme nothing that names the host, the repo, or its files', () => {
    const [spec] = layoutTerraces(8, [repo('secret-repo', 4)])
    const { theme, given } = recordingTheme()
    placeTerrace(theme, spec, 7)
    expect(JSON.stringify(given)).not.toContain('secret')
    expect(given[0]).toEqual({ sx: spec.sx, sz: spec.sz, color: ropeColor(spec.id) })
    expect(given.slice(1)).toHaveLength(4)
    expect(given.slice(1).every((h) => typeof h === 'number')).toBe(true)
  })

  it('places the terrace, and stands a building per file on its lot with what a click on it means', () => {
    const spec = layoutTerraces(8, [repo('r1', 3), repo('r2', 16)])[1]
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
