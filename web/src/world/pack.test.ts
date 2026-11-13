import * as THREE from 'three'
import { describe, expect, it, vi } from 'vitest'

vi.stubGlobal('window', { matchMedia: () => ({ matches: false }) })
const { GLTFLoader } = await import('three/examples/jsm/loaders/GLTFLoader')
const { packTheme } = await import('./pack')
const { PROVIDER_COLOR } = await import('./airship')
type PackManifest = import('./pack').PackManifest

/**
 * A GLB with one triangle drawn as every model the manifest names. Every
 * node's extras try to say what a click on it means, which no pack may.
 */
function glb(): ArrayBuffer {
  const tri = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0])
  const extras = { host: 'elsewhere', terraceId: 'other-repo', bldIdx: 3, agentId: 'someone' }
  const node = (name: string, extra: Record<string, unknown> = {}) => ({ name, mesh: 0, extras, ...extra })
  const nodes = [
    node('citadel', { children: [1, 2, 3] }),
    node('core', { translation: [0, -12, 0] }),
    node('halo', { translation: [0, 9, 0] }),
    node('beacon', { translation: [0, 9, 0] }),
    node('island', { children: [5] }),
    node('core.001', { translation: [0, -1.25, 0] }),
    node('ring'),
    node('plaza', { mesh: 1 }),
    node('post', { mesh: 1 }),
    node('building', { children: [10] }),
    node('cap'),
    node('crane', { mesh: 2, children: [12] }),
    node('bag', { translation: [0, -0.6, 0] }),
  ]
  const prim = (material: number) => ({ attributes: { POSITION: 0 }, material })
  const doc = {
    asset: { version: '2.0' },
    scene: 0,
    scenes: [{ nodes: [0, 4, 6, 7, 8, 9, 11] }],
    nodes,
    meshes: [{ primitives: [prim(0)] }, { primitives: [prim(1), prim(2)] }, { primitives: [prim(3)] }],
    materials: [{ name: 'paper' }, { name: 'repo' }, { name: 'repo-glow', emissiveFactor: [1, 1, 1] }, { name: 'provider.001', emissiveFactor: [1, 1, 1] }],
    accessors: [{ bufferView: 0, componentType: 5126, count: 3, type: 'VEC3', min: [0, 0, 0], max: [1, 1, 0] }],
    bufferViews: [{ buffer: 0, byteLength: 36 }],
    buffers: [{ byteLength: 36 }],
  }
  let json = new TextEncoder().encode(JSON.stringify(doc))
  const pad = (n: number) => (4 - (n % 4)) % 4
  json = Uint8Array.from([...json, ...Array(pad(json.length)).fill(0x20)])
  const bin = new Uint8Array(tri.buffer)
  const total = 12 + 8 + json.length + 8 + bin.length
  const out = new DataView(new ArrayBuffer(total))
  let at = 0
  const u32 = (v: number) => {
    out.setUint32(at, v, true)
    at += 4
  }
  u32(0x46546c67)
  u32(2)
  u32(total)
  u32(json.length)
  u32(0x4e4f534a)
  new Uint8Array(out.buffer, at, json.length).set(json)
  at += json.length
  u32(bin.length)
  u32(0x004e4942)
  new Uint8Array(out.buffer, at, bin.length).set(bin)
  return out.buffer
}

const material = (color: string) => ({ color })
const manifest: PackManifest = {
  format: 1,
  id: 'test-pack',
  name: 'Test Pack',
  version: '1.0.0',
  sky: null,
  buildings: { rest: material('#eeeeee'), read: material('#ddddff'), edit: material('#ffeecc'), conflict: material('#ffcccc'), selected: material('#eeffff') },
  citadel: { model: 'citadel', labelY: 12, ringRadius: 10, beacon: '#ffd27a' },
  island: { models: ['island'], scenery: [{ model: 'ring', perRadius: 1, pad: 0.5, scale: [1, 1] }] },
  ring: { model: 'ring', spacing: 1, spin: 0.3 },
  terrace: { plaza: 'plaza', post: 'post', postSpacing: 1, postOut: 0.2, glow: 0.5 },
  building: { model: 'building' },
  airship: { model: 'crane' },
}

async function theme() {
  const gltf = await new Promise<import('three/examples/jsm/loaders/GLTFLoader').GLTF>((resolve, reject) => new GLTFLoader().parse(glb(), '', resolve, reject))
  return packTheme(manifest, gltf)
}

/** Every userData key anywhere under an object. */
function keys(o: THREE.Object3D): Set<string> {
  const found = new Set<string>()
  o.traverse((x) => Object.keys(x.userData).forEach((k) => found.add(k)))
  return found
}

describe('packTheme', () => {
  it('lets nothing in the file say what a click means', async () => {
    const t = await theme()
    for (const o of [t.buildCitadel().group, t.buildIsland({ R: 8, depth: 10, seed: 3, phase: 0 }).group, t.buildTerrace({ sx: 3, sz: 2, color: 0xff0000 }).group, t.building(2), t.airship('claude').group]) {
      for (const k of ['host', 'terraceId', 'bldIdx', 'agentId']) expect(keys(o)).not.toContain(k)
    }
  })

  it('stretches an island to its shape but not its core', async () => {
    const isl = (await theme()).buildIsland({ R: 8, depth: 10, seed: 3, phase: 0 })
    expect(isl.core.local.y).toBeCloseTo(-12.5)
    expect(isl.core.mesh.getWorldScale(new THREE.Vector3()).toArray()).toEqual([1, 1, 1])
    // Scenery stands straight in the island group, where the world hides what a terrace covers.
    expect(isl.group.children.filter((c) => c.userData.scenery === 0.5)).toHaveLength(8)
  })

  it('stands a cap on a building at the size it was drawn', async () => {
    const b = (await theme()).building(2)
    const cap = b.children[0]
    expect(cap.position.y).toBe(0.5)
    expect(cap.scale.y).toBeCloseTo(0.35)
  })

  it('tints an airship in its provider’s color, in materials of its own', async () => {
    const t = await theme()
    const a = t.airship('codex')
    const b = t.airship('gemini')
    const tinted = (s: typeof a) => s.mats.map((m) => (m as THREE.MeshStandardMaterial).color.getHex())
    expect(tinted(a)).toContain(PROVIDER_COLOR.codex)
    expect(tinted(b)).toContain(PROVIDER_COLOR.gemini)
    expect(a.mats.some((m) => b.mats.includes(m))).toBe(false)
    expect(a.bags).toHaveLength(1)
  })

  it('tints a terrace in its repo’s color and brightens its glow while selected', async () => {
    const t = (await theme()).buildTerrace({ sx: 3, sz: 2, color: 0x3fa37c })
    const mats = new Set<THREE.MeshStandardMaterial>()
    t.group.traverse((o) => {
      const m = (o as THREE.Mesh).material as THREE.MeshStandardMaterial | undefined
      if (m?.name.startsWith('repo')) mats.add(m)
    })
    expect([...mats].every((m) => m.color.getHex() === 0x3fa37c)).toBe(true)
    const glow = [...mats].find((m) => m.name === 'repo-glow') as THREE.MeshStandardMaterial
    t.animate(1, false)
    expect(glow.emissiveIntensity).toBeCloseTo(0.5)
    t.animate(1.6, true)
    expect(glow.emissiveIntensity).toBeGreaterThan(0.5)
  })
})
