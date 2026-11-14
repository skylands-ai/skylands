import * as THREE from 'three'
import type { GLTF } from 'three/examples/jsm/loaders/GLTFLoader'
import { reduceMotion } from '../dom'
import type { ThemeInfo } from '../protocol'
import { PROVIDER_COLOR, type Airship, type Provider } from './airship'
import { rng } from './noise'
import type { Crystal } from './shapes'
import { boundaryPoints } from './terrace'
import { markShared, type BuildingMats, type Citadel, type IslandMeshes, type IslandShape, type SelectionRing, type SkyLook, type TerraceMeshes, type TerracePlot, type Visitor, type WorldTheme } from './theme'
import { viewAt } from './visitors'

/**
 * A theme pack's theme.json, format 1, which the citadel checked before
 * offering it. The skylands-ai/themes repository's FORMAT.md describes it.
 */
export interface PackManifest {
  format: 1
  id: string
  name: string
  version: string
  sky: PackSky | null
  buildings: Record<keyof BuildingMats, PackMaterial>
  citadel: { model: string; labelY: number; ringRadius: number; beacon: string }
  island: { models: string[]; scenery?: { model: string; perRadius: number; pad: number; scale: [number, number] }[] }
  ring: { model: string; spacing: number; spin: number }
  terrace: { plaza: string; post: string; postSpacing: number; postOut: number; glow: number; rope?: PackRope }
  building: { model: string }
  airship: { model: string; providers?: Partial<Record<Provider, string>> }
  visitor?: { model: string; path: 'glide'; duration: number }
}

interface PackSky {
  top: string
  horizon: string
  fog: string
  fogNear: number
  fogFar: number
  stars: number
  starsAround: boolean
  clouds: boolean
  hemiSky: string
  hemiGround: string
  hemiIntensity: number
  sun: string
  sunIntensity: number
}

/**
 * What the world hangs from each of a terrace's posts to the next: a tube
 * radius thick from the point at in each post's own space, its middle sag
 * below a straight line, in the material of models.glb named material,
 * with a bead strung along it about spacing apart if there is one.
 */
interface PackRope {
  at: [number, number, number]
  sag: number
  radius: number
  material: string
  bead?: { model: string; spacing: number }
}

interface PackMaterial {
  color: string
  emissive?: string
  emissiveIntensity?: number
  roughness?: number
  metalness?: number
}

/** Where a pack is in loading: loading, drawn from, or refused and why. */
export type PackEntry = { state: 'loading' } | { state: 'ready'; theme: WorldTheme } | { state: 'failed'; problem: string }

const entries = new Map<string, PackEntry>()

/**
 * Where an installed pack is in loading, starting it the first time it is
 * asked for; settled is told once it is drawn from or refused. A pack is
 * loaded once per page, by its id and hash, so one replaced since is a new
 * pack.
 */
export function packEntry(info: ThemeInfo, settled: (problem: string | null) => void): PackEntry {
  const key = `${info.id}@${info.hash}`
  const had = entries.get(key)
  if (had) return had
  const e: PackEntry = { state: 'loading' }
  entries.set(key, e)
  loadPack(info).then(
    (theme) => {
      entries.set(key, { state: 'ready', theme })
      settled(null)
    },
    (err: unknown) => {
      const problem = err instanceof Error ? err.message : String(err)
      console.error(`world: theme pack ${info.id} refused`, err)
      entries.set(key, { state: 'failed', problem })
      settled(problem)
    },
  )
  return e
}

/** Fetches a pack's files from the citadel that checked them, and makes a theme of them. */
async function loadPack(info: ThemeInfo): Promise<WorldTheme> {
  const base = `/themes/${encodeURIComponent(info.id)}/${encodeURIComponent(info.hash)}/`
  const [mres, gres] = await Promise.all([fetch(base + 'theme.json'), fetch(base + 'models.glb')])
  if (!mres.ok || !gres.ok) throw new Error(`the citadel answered ${mres.ok ? gres.status : mres.status} for its files`)
  const manifest = (await mres.json()) as PackManifest
  if (manifest.format !== 1 || manifest.id !== info.id) throw new Error('its theme.json isn’t format 1 for this pack')
  const data = await gres.arrayBuffer()
  const { GLTFLoader } = await import('three/examples/jsm/loaders/GLTFLoader')
  // Everything a pack draws is in models.glb, which the citadel checked
  // names no URL. Should anything still ask the loader for one, it gets
  // nothing; only the blobs the loader makes of the file's own images load.
  const manager = new THREE.LoadingManager()
  manager.setURLModifier((url) => (url.startsWith('blob:') ? url : 'data:,'))
  const gltf = await new Promise<GLTF>((resolve, reject) => new GLTFLoader(manager).parse(data, '', resolve, reject))
  return packTheme(manifest, gltf)
}

const TAU = Math.PI * 2
const UP = new THREE.Vector3(0, 1, 0)

/** A name up to its first dot, so core.001, as Blender names a second core, is a core too. */
const stem = (name: string): string => name.split('.')[0]

/** A node's name as the pack wrote it; three.js keeps it here and changes .name. */
const original = (o: THREE.Object3D): string => (typeof o.userData.name === 'string' ? o.userData.name : o.name)

const hex = (c: string): number => parseInt(c.slice(1), 16)

/** Every material on a mesh. */
const materialsOf = (o: THREE.Object3D): THREE.Material[] => {
  const m = (o as THREE.Mesh).material
  return Array.isArray(m) ? m : m ? [m] : []
}

/** Tubes made alike, each with positions, normals, and uvs, as one geometry, freeing the tubes. */
function joined(geos: THREE.BufferGeometry[]): THREE.BufferGeometry {
  const out = new THREE.BufferGeometry()
  for (const [name, size] of [
    ['position', 3],
    ['normal', 3],
    ['uv', 2],
  ] as const) {
    const parts = geos.map((g) => g.getAttribute(name).array as Float32Array)
    const all = new Float32Array(parts.reduce((n, a) => n + a.length, 0))
    let at = 0
    for (const a of parts) {
      all.set(a, at)
      at += a.length
    }
    out.setAttribute(name, new THREE.BufferAttribute(all, size))
  }
  const index: number[] = []
  let base = 0
  for (const g of geos) {
    const idx = (g.index as THREE.BufferAttribute).array
    for (let i = 0; i < idx.length; i++) index.push(idx[i] + base)
    base += g.getAttribute('position').count
    g.dispose()
  }
  out.setIndex(index)
  return out
}

/** The world's materials for a link marker, which it colors by the link's state. */
function linkMaterial(): THREE.MeshStandardMaterial {
  return new THREE.MeshStandardMaterial({ color: 0x7fe0f0, emissive: 0x3fc9e0, emissiveIntensity: 1.1, flatShading: true, roughness: 0.92, metalness: 0 })
}

/**
 * Makes a theme of a checked pack. The world places, tags, and moves what
 * it draws, as for any theme; a pack only decides what things look like,
 * from the parts its models name and the motions its clips make.
 */
export function packTheme(m: PackManifest, gltf: GLTF): WorldTheme {
  // Nothing the pack's file says reaches what the world reads off objects,
  // such as what a click on one means, so only names are kept.
  gltf.scene.traverse((o) => {
    o.userData = typeof o.userData.name === 'string' ? { name: o.userData.name } : {}
    for (const mat of materialsOf(o)) mat.userData = {}
    const g = (o as THREE.Mesh).geometry
    if (g) g.userData = {}
  })
  // What the file holds is shared by every copy of a model, so the world
  // never frees it with one of them.
  gltf.scene.traverse((o) => {
    const g = (o as THREE.Mesh).geometry
    markShared([...(g ? [g] : []), ...materialsOf(o)])
  })

  const roots = new Map<string, THREE.Object3D>()
  for (const o of gltf.scene.children) roots.set(original(o), o)
  const root = (name: string): THREE.Object3D => {
    const o = roots.get(name)
    if (!o) throw new Error(`models.glb has no ${name}`)
    return o
  }

  // The clips that move only what is in a model play on every copy of it.
  const clips = new Map<THREE.Object3D, THREE.AnimationClip[]>()
  const clipsOf = (src: THREE.Object3D): THREE.AnimationClip[] => {
    let cs = clips.get(src)
    if (!cs) {
      const names = new Set<string>()
      src.traverse((o) => names.add(o.name))
      cs = gltf.animations.filter((c) => c.tracks.length > 0 && c.tracks.every((t) => names.has(THREE.PropertyBinding.parseTrackName(t.name).nodeName)))
      clips.set(src, cs)
    }
    return cs
  }

  /** A copy of a model, and how to move it on by dt seconds. at offsets its clips, so copies don't move in step. */
  const copy = (name: string, at = 0): { object: THREE.Object3D; step: (dt: number) => void } => {
    const src = root(name)
    const object = src.clone(true)
    const cs = clipsOf(src)
    if (!cs.length) return { object, step: () => {} }
    const mixer = new THREE.AnimationMixer(object)
    for (const c of cs) mixer.clipAction(c).play()
    mixer.setTime(at)
    return {
      object,
      step: (dt) => {
        if (!reduceMotion) mixer.update(dt)
      },
    }
  }

  const parts = (o: THREE.Object3D, name: string): THREE.Object3D[] => {
    const found: THREE.Object3D[] = []
    o.traverse((x) => {
      if (x !== o && stem(original(x)) === name) found.push(x)
    })
    return found
  }
  const part = (o: THREE.Object3D, name: string, model: string): THREE.Object3D => {
    const p = parts(o, name)[0]
    if (!p) throw new Error(`${model} has no ${name}`)
    return p
  }
  const firstMesh = (o: THREE.Object3D): THREE.Mesh | null => {
    let found: THREE.Mesh | null = null
    o.traverse((x) => {
      if (!found && (x as THREE.Mesh).isMesh) found = x as THREE.Mesh
    })
    return found
  }

  /** Gives a copy its own materials, the same copy for each it shared, and returns them. */
  const ownMaterials = (o: THREE.Object3D, which: (mat: THREE.Material) => boolean = () => true, mine = new Map<THREE.Material, THREE.Material>()): THREE.Material[] => {
    o.traverse((x) => {
      const mesh = x as THREE.Mesh
      if (!mesh.isMesh) return
      const swap = (mat: THREE.Material): THREE.Material => {
        if (!which(mat)) return mat
        let own = mine.get(mat)
        if (!own) {
          own = mat.clone()
          own.userData = {}
          mine.set(mat, own)
        }
        return own
      }
      mesh.material = Array.isArray(mesh.material) ? mesh.material.map(swap) : swap(mesh.material)
    })
    return [...mine.values()]
  }

  /** Moves a part out of a model that the world stretches, to where it is in the group, keeping its own size. */
  const lift = (p: THREE.Object3D, g: THREE.Group): THREE.Vector3 => {
    g.updateMatrixWorld(true)
    const at = g.worldToLocal(p.getWorldPosition(new THREE.Vector3()))
    p.parent?.remove(p)
    p.position.copy(at)
    g.add(p)
    return at
  }

  /** A link marker: the part the world turns and colors by the link's state, with its light. */
  const crystal = (p: THREE.Object3D, g: THREE.Group, at: THREE.Vector3): Crystal => {
    const mat = linkMaterial()
    p.traverse((x) => {
      if ((x as THREE.Mesh).isMesh) (x as THREE.Mesh).material = mat
    })
    const light = new THREE.PointLight(0x7fe0f0, 1.3, 16, 2)
    light.position.copy(at)
    g.add(light)
    return { mesh: p, mat, light, local: at.clone() }
  }

  const ring = (radius: number): SelectionRing => {
    const g = new THREE.Group()
    const n = Math.max(6, Math.round((TAU * radius) / m.ring.spacing))
    const steps: ((dt: number) => void)[] = []
    for (let i = 0; i < n; i++) {
      const a = (i / n) * TAU
      const piece = copy(m.ring.model, i * 0.37)
      piece.object.traverse((x) => (x.raycast = () => {}))
      const at = new THREE.Group()
      // The piece's +x points out from the ring's center.
      at.position.set(Math.cos(a) * radius, 0, Math.sin(a) * radius)
      at.rotation.y = -a
      at.add(piece.object)
      g.add(at)
      steps.push(piece.step)
    }
    return {
      group: g,
      animate(_time, dt) {
        if (!reduceMotion) g.rotation.y += dt * m.ring.spin
        for (const s of steps) s(dt)
      },
    }
  }

  const material = (p: PackMaterial, flat: boolean): THREE.MeshStandardMaterial =>
    new THREE.MeshStandardMaterial({
      color: hex(p.color),
      emissive: p.emissive ? hex(p.emissive) : 0,
      emissiveIntensity: p.emissiveIntensity ?? 0,
      roughness: p.roughness ?? 0.92,
      metalness: p.metalness ?? 0,
      flatShading: flat,
    })

  // A building's body is a unit block, which the world stretches.
  const bldSrc = root(m.building.model) as THREE.Mesh
  if (!bldSrc.isMesh) throw new Error(`${m.building.model} isn’t one mesh`)
  const bldGeo = bldSrc.geometry.clone()
  bldGeo.computeBoundingBox()
  const box = bldGeo.boundingBox as THREE.Box3
  const size = box.getSize(new THREE.Vector3())
  const mid = box.getCenter(new THREE.Vector3())
  bldGeo.translate(-mid.x, -mid.y, -mid.z)
  bldGeo.scale(1 / Math.max(size.x, 1e-6), 1 / Math.max(size.y, 1e-6), 1 / Math.max(size.z, 1e-6))
  const flatBld = !bldGeo.getAttribute('normal')
  const b = m.buildings
  const buildings: BuildingMats = {
    rest: material(b.rest, flatBld),
    read: material(b.read, flatBld),
    edit: material(b.edit, flatBld),
    conflict: material(b.conflict, flatBld),
    selected: material(b.selected, flatBld),
  }
  markShared([bldGeo, ...Object.values(buildings)])

  // Each model is checked once, now, so a pack missing one is refused
  // before it draws anything.
  for (const name of [m.citadel.model, ...m.island.models, ...(m.island.scenery ?? []).map((s) => s.model), m.ring.model, m.terrace.plaza, m.terrace.post, m.airship.model, ...Object.values(m.airship.providers ?? {}), ...(m.visitor ? [m.visitor.model] : []), ...(m.terrace.rope?.bead ? [m.terrace.rope.bead.model] : [])]) {
    if (name) root(name)
  }

  // A rope is drawn in the material of that name that a model uses.
  const rope = m.terrace.rope
  let ropeMat: THREE.Material | null = null
  if (rope) {
    const want = stem(rope.material)
    gltf.scene.traverse((o) => {
      for (const mat of materialsOf(o)) if (!ropeMat && stem(mat.name) === want) ropeMat = mat
    })
    if (!ropeMat) throw new Error(`no model in models.glb uses a material named ${rope.material}`)
  }

  const sky: SkyLook | null = m.sky
    ? {
        top: hex(m.sky.top),
        horizon: hex(m.sky.horizon),
        fog: hex(m.sky.fog),
        fogNear: m.sky.fogNear,
        fogFar: m.sky.fogFar,
        stars: m.sky.stars,
        starsAround: m.sky.starsAround,
        clouds: m.sky.clouds,
        hemiSky: hex(m.sky.hemiSky),
        hemiGround: hex(m.sky.hemiGround),
        hemiIntensity: m.sky.hemiIntensity,
        sun: hex(m.sky.sun),
        sunIntensity: m.sky.sunIntensity,
      }
    : null

  const theme: WorldTheme = {
    id: m.id,
    sky,
    buildings,

    buildCitadel(): Citadel {
      const g = new THREE.Group()
      const body = copy(m.citadel.model)
      g.add(body.object)
      const name = m.citadel.model
      const coreP = part(body.object, 'core', name)
      const core = crystal(coreP, g, lift(coreP, g))
      const haloMesh = firstMesh(part(body.object, 'halo', name))
      if (!haloMesh) throw new Error(`${name}'s halo has no mesh`)
      const was = materialsOf(haloMesh)[0] as THREE.MeshStandardMaterial | undefined
      const haloMat = new THREE.MeshBasicMaterial({ color: was?.color ?? hex(m.citadel.beacon), transparent: true, opacity: 0.4, depthWrite: false })
      haloMesh.material = haloMat
      haloMesh.raycast = () => {}
      const beacon = part(body.object, 'beacon', name)
      g.updateMatrixWorld(true)
      const light = new THREE.PointLight(hex(m.citadel.beacon), 1.1, 30, 2)
      light.position.copy(g.worldToLocal(beacon.getWorldPosition(new THREE.Vector3())))
      g.add(light)
      const sel = ring(m.citadel.ringRadius)
      sel.group.position.y = 0.1
      g.add(sel.group)
      return {
        group: g,
        core,
        ring: sel,
        halo: haloMesh as THREE.Mesh<THREE.SphereGeometry, THREE.MeshBasicMaterial>,
        light,
        labelY: m.citadel.labelY,
        animate: (_time, dt) => body.step(dt),
      }
    },

    buildIsland(h: IslandShape): IslandMeshes {
      const g = new THREE.Group()
      const r = rng(h.seed * 7 + 3)
      const models = m.island.models
      const name = models[h.seed % models.length]
      const body = copy(name)
      // The model is drawn at radius 1 reaching down to -1; the world
      // stretches it to the island, but not the parts it names.
      const shape = new THREE.Group()
      shape.scale.set(h.R, h.depth, h.R)
      shape.rotation.y = r() * TAU
      shape.add(body.object)
      g.add(shape)
      const coreP = part(body.object, 'core', name)
      const core = crystal(coreP, g, lift(coreP, g))
      const steps = [body.step]
      for (const kind of m.island.scenery ?? []) {
        const [lo, hi] = kind.scale
        for (let n = Math.round(h.R * kind.perRadius); n > 0; n--) {
          const a = r() * TAU
          const d = Math.sqrt(r()) * 0.84 * h.R
          const thing = copy(kind.model, r() * 10)
          const at = new THREE.Group()
          at.position.set(Math.cos(a) * d, 0, Math.sin(a) * d)
          at.rotation.y = r() * TAU
          at.scale.setScalar(lo + (hi - lo) * r())
          // Scenery under a terrace is hidden, with this much room around it.
          at.userData.scenery = kind.pad
          at.add(thing.object)
          g.add(at)
          steps.push(thing.step)
        }
      }
      const sel = ring(h.R * 1.18)
      sel.group.position.y = 0.15
      g.add(sel.group)
      return {
        group: g,
        core,
        ring: sel,
        animate(_time, dt) {
          for (const s of steps) s(dt)
        },
      }
    },

    buildTerrace(t: TerracePlot): TerraceMeshes {
      const g = new THREE.Group()
      const glow: THREE.MeshStandardMaterial[] = []
      const mine = new Map<THREE.Material, THREE.Material>()
      // repo is the repo's color, and repo-glow glows in it too.
      const tint = (o: THREE.Object3D): void => {
        for (const mat of ownMaterials(o, (x) => stem(x.name) === 'repo' || stem(x.name) === 'repo-glow', mine)) {
          const s = mat as THREE.MeshStandardMaterial
          if (!s.color) continue
          s.color.set(t.color)
          if (stem(s.name) === 'repo-glow' && s.emissive) {
            s.emissive.set(t.color)
            s.emissiveIntensity = m.terrace.glow
            if (!glow.includes(s)) glow.push(s)
          }
        }
      }
      const plaza = copy(m.terrace.plaza)
      const floor = new THREE.Group()
      floor.scale.set(t.sx, 1, t.sz)
      floor.add(plaza.object)
      g.add(floor)
      tint(plaza.object)
      const steps = [plaza.step]
      const out = m.terrace.postOut
      const hx = t.sx / 2 + out
      const ends: THREE.Vector3[] = []
      boundaryPoints(t, out, m.terrace.postSpacing).forEach(([x, z], i) => {
        const post = copy(m.terrace.post, i * 0.61)
        tint(post.object)
        const at = new THREE.Group()
        at.position.set(x, 0, z)
        // The post's +x points out from the plaza, across the edge it stands on.
        const [nx, nz] = Math.abs(Math.abs(x) - hx) < 1e-6 ? [Math.sign(x), 0] : [0, Math.sign(z)]
        at.rotation.y = -Math.atan2(nz, nx)
        at.add(post.object)
        g.add(at)
        steps.push(post.step)
        // Where this post's rope ends, in the terrace.
        if (rope) ends.push(new THREE.Vector3(...rope.at).applyAxisAngle(UP, at.rotation.y).add(at.position))
      })
      if (rope && ropeMat) {
        // From each post to the next, round the terrace. A quadratic
        // curve's middle is halfway to its control point, so the control
        // point is twice the sag below the middle of the span.
        const spans = ends.map((a, i) => {
          const b = ends[(i + 1) % ends.length]
          const mid = a.clone().lerp(b, 0.5)
          mid.y -= 2 * rope.sag
          return new THREE.QuadraticBezierCurve3(a, mid, b)
        })
        const line = new THREE.Mesh(joined(spans.map((c) => new THREE.TubeGeometry(c, 12, rope.radius, 5, false))), ropeMat)
        line.name = 'rope'
        tint(line)
        g.add(line)
        if (rope.bead) {
          // Beads at even steps along each span, none on a post, upright
          // with +z along the rope.
          const placed: THREE.Matrix4[] = []
          const turn = new THREE.Quaternion()
          const one = new THREE.Vector3(1, 1, 1)
          for (const c of spans) {
            const n = Math.max(1, Math.round(c.getLength() / rope.bead.spacing))
            for (let k = 0; k < n; k++) {
              const u = (k + 0.5) / n
              const along = c.getTangentAt(u)
              turn.setFromAxisAngle(UP, Math.atan2(along.x, along.z))
              placed.push(new THREE.Matrix4().compose(c.getPointAt(u), turn, one))
            }
          }
          // Each part of the bead is one instanced mesh, so a terrace's
          // beads are a few draws. Beads don't move.
          const src = root(rope.bead.model)
          src.updateMatrixWorld(true)
          const each = new THREE.Matrix4()
          src.traverse((x) => {
            const part = x as THREE.Mesh
            if (!part.isMesh) return
            const beads = new THREE.InstancedMesh(part.geometry, part.material, placed.length)
            placed.forEach((p, i) => beads.setMatrixAt(i, each.multiplyMatrices(p, part.matrixWorld)))
            beads.instanceMatrix.needsUpdate = true
            // Its bounds are one bead's at the terrace's middle, wherever its beads are.
            beads.frustumCulled = false
            beads.name = 'bead'
            tint(beads)
            g.add(beads)
          })
        }
      }
      let last = -1
      return {
        group: g,
        animate(time, selected) {
          const dt = last < 0 ? 0 : Math.min(0.05, Math.max(0, time - last))
          last = time
          for (const s of steps) s(dt)
          const k = m.terrace.glow
          const level = selected ? (reduceMotion ? k + 0.45 : k + 0.35 + 0.35 * Math.sin(time * 2.6)) : k
          for (const mat of glow) if (mat.emissiveIntensity !== level) mat.emissiveIntensity = level
        },
      }
    },

    building(height: number): THREE.Mesh {
      const bld = new THREE.Mesh(bldGeo, buildings.rest)
      for (const child of bldSrc.children) {
        const c = child.clone(true)
        if (stem(original(c)) === 'cap') {
          // The world stretches the body to 0.7 by height by 0.7; the cap
          // sits on its roof, at the size it was drawn for a unit block.
          c.position.set(c.position.x, 0.5, c.position.z)
          c.scale.y *= 0.7 / height
        }
        bld.add(c)
      }
      return bld
    },

    airship(prov: Provider): Airship {
      const name = m.airship.providers?.[prov] ?? m.airship.model
      const ship = copy(name, Math.random() * 5)
      const g = new THREE.Group()
      g.add(ship.object)
      const color = PROVIDER_COLOR[prov]
      // Every material is the ship's own, so fading it never dims another.
      const mats = ownMaterials(ship.object)
      for (const mat of mats) {
        const s = mat as THREE.MeshStandardMaterial
        if (stem(s.name) !== 'provider' || !s.color) continue
        s.color.set(color)
        if (s.emissive) {
          s.emissive.set(color)
          s.emissiveIntensity = 0.12
        }
      }
      const bags = parts(ship.object, 'bag').filter((x) => (x as THREE.Mesh).isMesh) as THREE.Mesh[]
      return { group: g, mats, bags, animate: ship.step }
    },

    visitor: m.visitor ? () => glide(copy((m.visitor as NonNullable<PackManifest['visitor']>).model), (m.visitor as NonNullable<PackManifest['visitor']>).duration) : undefined,
  }
  return theme
}

/**
 * The glide path: across the upper part of the view, rising or sinking a
 * little and nearing or leaving the camera, facing where it goes, which
 * is the model's +z.
 */
function glide(thing: { object: THREE.Object3D; step: (dt: number) => void }, duration: number): Visitor {
  const group = new THREE.Group()
  group.add(thing.object)
  const from = new THREE.Vector3()
  const to = new THREE.Vector3()
  const up = new THREE.Vector3()
  const ahead = new THREE.Vector3()
  let last = -1
  return {
    object: group,
    duration,
    begin(camera) {
      const d = 30 + Math.random() * 14
      const dz = (Math.random() - 0.5) * 18
      const v0 = viewAt(camera, d + dz)
      const v1 = viewAt(camera, d - dz)
      const side = Math.random() < 0.5 ? -1 : 1
      const y0 = 0.15 + Math.random() * 0.5
      const y1 = y0 + (Math.random() - 0.5) * 0.3
      from.copy(v0.center).addScaledVector(v0.right, -side * 1.2 * v0.halfW).addScaledVector(v0.up, y0 * v0.halfH)
      to.copy(v1.center).addScaledVector(v1.right, side * 1.2 * v1.halfW).addScaledVector(v1.up, y1 * v1.halfH)
      up.copy(v0.up)
      last = -1
    },
    step(t, time) {
      group.position.lerpVectors(from, to, t).addScaledVector(up, Math.sin(t * Math.PI * 2) * 0.4)
      group.lookAt(ahead.subVectors(to, from).add(group.position))
      thing.step(last < 0 ? 0 : Math.min(0.05, Math.max(0, time - last)))
      last = time
    },
  }
}
