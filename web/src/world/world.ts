import * as THREE from 'three'
import { css, el, reduceMotion } from '../dom'
import type { Repo } from '../protocol'
import { level, type Nav } from '../store'
import { Agents, bldWorld, refreshBuilding, type AgentState, type TerraceAccess } from './agents'
import { CameraRig, HOME } from './camera'
import { buildCitadel, type Citadel } from './citadel'
import { attachPointerInput } from './input'
import { buildIsland } from './island'
import { Labels } from './labels'
import { M } from './materials'
import { placeIsland, type Placement } from './placement'
import type { Crystal } from './shapes'
import { Sky } from './sky'
import { buildTerrace, disposeTerrace, layoutTerraces, ROPE_GLOW, type TerraceMeshes, type TerraceSpec } from './terrace'
import { Tethers } from './tethers'

/** Text and a CSS color for a status dot. */
export type Status = [text: string, color: string]

/** Whether an object and all its ancestors are visible. */
function isDrawn(o: THREE.Object3D | null): boolean {
  for (; o; o = o.parent) if (!o.visible) return false
  return true
}

interface TerraceView {
  spec: TerraceSpec
  meshes: TerraceMeshes
  /** Building index by repo-relative path, for the files that got one. */
  pathIdx: Map<string, number>
}

interface IslandView {
  id: string
  def: Placement
  group: THREE.Group
  core: Crystal
  ring: THREE.Group
  pos: THREE.Vector3
  /** 0 while reachable, easing to 1 while the citadel can't reach the host. */
  drift: number
  reachable: boolean
  label: HTMLButtonElement
  terraces: Map<string, TerraceView>
  /** What the terraces were built from, so unchanged repos are not rebuilt. */
  terrainKey: string
}

/** What a click landed on. */
interface Pick {
  host: string
  terrace: string | null
  bld: number | null
  agent: string | null
  point: THREE.Vector3
}

const tmpW = new THREE.Vector3()

/**
 * The 3D world: renderer, scene, camera, the citadel, the islands with their
 * terraces, and the frame loop. It knows nothing about NATS or daemons. The
 * page tells it which islands and repos exist and whether each is reachable.
 */
export class World {
  readonly citadel: Citadel
  /** Where the camera is. */
  nav: Nav = { selected: 'cit', terrace: null, file: null, agent: null }
  /** Called whenever navigation changes, from a click in the world or a label. */
  onNav: (n: Nav) => void = () => {}

  private readonly renderer: THREE.WebGLRenderer
  private readonly scene = new THREE.Scene()
  /** The camera rig. Exposed for debugging and screenshots. */
  readonly rig = new CameraRig()
  private readonly sky: Sky
  private readonly tethers: Tethers
  private readonly labels: Labels
  private readonly citLabel: HTMLButtonElement
  private readonly agents: Agents
  /** The building currently pulsing as selected, so it can get its normal color back. */
  private selectedBld: THREE.Mesh | null = null
  private readonly islands = new Map<string, IslandView>()
  private readonly pickRoots: THREE.Object3D[] = []
  private readonly raycaster = new THREE.Raycaster()
  private readonly ndc = new THREE.Vector2()
  private beaconOn = false
  /** True while the page can't reach the citadel: nothing in the scene is current. */
  private stale = false
  /** Whether the citadel's crystal, the world's NATS server, is known to be up. */
  private crystalLit = false
  private time = 0
  private frameNo = 0
  private last = performance.now()

  /** Throws when WebGL is unavailable, so the page can show a message instead of a blank stage. */
  constructor(
    private readonly stage: HTMLElement,
    private readonly canvas: HTMLCanvasElement,
    labelsEl: HTMLElement,
  ) {
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true })
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
    this.sky = new Sky(this.scene)
    this.tethers = new Tethers(this.scene)
    this.citadel = buildCitadel()
    this.scene.add(this.citadel.group)
    this.pickRoots.push(this.citadel.group)
    this.labels = new Labels(labelsEl, stage, this.rig.camera)
    this.citLabel = this.labels.create(() => this.select('cit'))
    this.agents = new Agents(labelsEl, this.labels, (host, terrace) => this.terraceAccess(host, terrace))
    this.agents.onPick = (host, terrace, id) => this.selectAgent(host, terrace, id)

    if (window.ResizeObserver) new ResizeObserver(() => this.resize()).observe(stage)
    window.addEventListener('resize', () => this.resize())
    this.resize()

    this.applyTheme()
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    mq?.addEventListener('change', () => this.applyTheme())
    new MutationObserver(() => this.applyTheme()).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] })

    attachPointerInput(canvas, this.rig, {
      onPick: (x, y, deep) => this.act(this.pickAt(x, y), deep),
      hasTarget: (x, y) => this.pickAt(x, y) !== null,
    })
    this.showRings()
  }

  /** Adds an island the first time a host is seen, and keeps its reachability current. */
  upsertIsland(id: string, reachable: boolean): void {
    let isl = this.islands.get(id)
    if (!isl) {
      const def = placeIsland(id, [...this.islands.values()].map((i) => i.def))
      const m = buildIsland(id, def)
      this.scene.add(m.group)
      this.pickRoots.push(m.group)
      this.tethers.add(id)
      // A new island starts drifted out and glides in, which is its rise into the sky.
      isl = {
        id,
        def,
        group: m.group,
        core: m.core,
        ring: m.ring,
        pos: new THREE.Vector3(),
        drift: 1,
        reachable,
        label: this.labels.create(() => this.select(id)),
        terraces: new Map(),
        terrainKey: '',
      }
      this.islands.set(id, isl)
      this.updateIslands(0)
    }
    isl.reachable = reachable
  }

  /** Builds or rebuilds a host's terraces from its repos. Unchanged repos are left as they are. */
  setRepos(host: string, repos: Repo[]): void {
    const isl = this.islands.get(host)
    if (!isl) return
    const key = repos.map((r) => `${r.id}:${r.snapshottedAt}:${r.totalFiles}`).join('|')
    if (key === isl.terrainKey) return
    isl.terrainKey = key
    for (const t of isl.terraces.values()) disposeTerrace(t.meshes)
    isl.terraces.clear()
    const specs = layoutTerraces(isl.def.R, repos)
    specs.forEach((spec, k) => {
      const meshes = buildTerrace(spec, isl.def.seed * 31 + k)
      isl.group.add(meshes.group)
      const pathIdx = new Map<string, number>()
      meshes.blds.forEach((_, i) => pathIdx.set(spec.files[i].path, i))
      isl.terraces.set(spec.id, { spec, meshes, pathIdx })
      this.agents.terraceRebuilt(host, spec.id)
    })
    // Scenery was placed before the terraces existed, so clear what they cover.
    for (const o of isl.group.children) {
      const kind = o.userData.scenery as string | undefined
      if (!kind) continue
      const pad = kind === 'hill' ? 1.6 : 0.7
      o.visible = !specs.some((t) => Math.abs(o.position.x - t.x) < t.sx / 2 + pad && Math.abs(o.position.z - t.z) < t.sz / 2 + pad)
    }
  }

  /** How many buildings a terrace shows, which can be fewer than the repo's files. */
  buildingCount(host: string, terrace: string): number {
    return this.islands.get(host)?.terraces.get(terrace)?.meshes.blds.length ?? 0
  }

  /** Gives a host's agents their airships, and takes them away as sessions end. */
  setAgents(host: string, states: AgentState[]): void {
    this.agents.set(host, states)
  }

  private terraceAccess(host: string, terrace: string): TerraceAccess | undefined {
    const isl = this.islands.get(host)
    const t = isl?.terraces.get(terrace)
    if (!isl || !t) return undefined
    return { island: isl.group, blds: t.meshes.blds, pathIdx: t.pathIdx, center: { x: t.spec.x, z: t.spec.z } }
  }

  /** Fills the floating label of the citadel or an island. */
  setLabel(id: string, name: string, status: Status, waiting: number): void {
    const b = id === 'cit' ? this.citLabel : this.islands.get(id)?.label
    if (!b) return
    b.textContent = ''
    b.appendChild(el('span', { class: 'nm' }, [name, waiting ? el('span', { class: 'badge', text: String(waiting) }) : null]))
    b.appendChild(el('span', { class: 'st' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]))
    b.setAttribute('aria-pressed', this.nav.selected === id ? 'true' : 'false')
    b.setAttribute('aria-label', `${name}, ${status[0]}${waiting ? `, ${waiting} waiting on you` : ''}. Opens the ${id === 'cit' ? 'citadel' : 'island'}.`)
  }

  /** Lights the beacon on the top tower while anything waits on a person. */
  setBeacon(on: boolean): void {
    this.beaconOn = on
  }

  /** Marks the whole scene as the last state heard: tethers dim and nothing flows. */
  setStale(on: boolean): void {
    this.stale = on
  }

  /** Lights the crystal under the castle while the citadel reports a live NATS connection. */
  setCitadelCrystal(lit: boolean): void {
    this.crystalLit = lit
  }

  /** Flies to the whole-world view. */
  overview(): void {
    this.setNav({ selected: 'cit', terrace: null, file: null, agent: null })
    this.rig.flyTo(HOME.target, HOME.radius, HOME.theta, HOME.phi)
  }

  /** Flies to the citadel or an island. */
  select(id: string): void {
    const isl = this.islands.get(id)
    if (id !== 'cit' && !isl) return
    this.setNav({ selected: id, terrace: null, file: null, agent: null })
    if (isl) {
      const h = isl.def
      const p = isl.pos
      this.rig.flyTo(new THREE.Vector3(p.x, p.y + 1.5, p.z), h.R * 2.6 + 16, h.angle, 1.0)
    } else this.rig.flyTo(new THREE.Vector3(0, 10, 0), 52, null, 0.95)
  }

  /** Flies down to a terrace. */
  land(host: string, terrace: string): void {
    const isl = this.islands.get(host)
    const t = isl?.terraces.get(terrace)
    if (!isl || !t) return
    this.setNav({ selected: host, terrace, file: null, agent: null })
    this.rig.flyTo(this.terraceWorld(isl, t), Math.max(t.spec.sx, t.spec.sz) * 1.6 + 6, isl.def.angle, 0.92)
  }

  /** Follows one agent's airship, close up. Without an airship it is the terrace view. */
  selectAgent(host: string, terrace: string, agent: string): void {
    const isl = this.islands.get(host)
    const t = isl?.terraces.get(terrace)
    if (!isl || !t) return
    this.setNav({ selected: host, terrace, file: null, agent })
    const target = new THREE.Vector3()
    if (this.agents.position(agent, target)) this.rig.flyTo(target, 9, null, 0.95)
    else this.rig.flyTo(this.terraceWorld(isl, t), Math.max(t.spec.sx, t.spec.sz) * 1.4 + 5, isl.def.angle, 0.95)
  }

  /** Flies up close to one building. */
  selectFile(host: string, terrace: string, idx: number): void {
    const t = this.islands.get(host)?.terraces.get(terrace)
    const b = t?.meshes.blds[idx]
    if (!t || !b) return
    this.setNav({ selected: host, terrace, file: idx, agent: null })
    this.rig.flyTo(bldWorld(b), 8, null, 0.95)
  }

  /** Esc: one level up. */
  up(): void {
    const n = this.nav
    switch (level(n)) {
      case 'agent':
      case 'file':
        this.land(n.selected, n.terrace as string)
        break
      case 'terrace':
        this.select(n.selected)
        break
      case 'island':
        this.overview()
        break
    }
  }

  start(): void {
    requestAnimationFrame((now) => this.frame(now))
  }

  private setNav(n: Nav): void {
    this.nav = n
    this.showRings()
    this.onNav({ ...n })
  }

  /** What a click does: one level deeper, or straight to the deepest thing on a double-click. */
  private act(p: Pick | null, deep: boolean): void {
    if (!p) return
    if (p.host === 'cit') {
      this.select('cit')
      return
    }
    if (p.agent && p.terrace) {
      this.selectAgent(p.host, p.terrace, p.agent)
      return
    }
    const sameHost = this.nav.selected === p.host
    if (p.bld !== null && p.terrace && (deep || (sameHost && this.nav.terrace === p.terrace))) {
      this.selectFile(p.host, p.terrace, p.bld)
      return
    }
    const tid = p.terrace ?? (deep ? this.nearestTerrace(p.host, p.point) : null)
    if (tid && (deep || sameHost)) {
      this.land(p.host, tid)
      return
    }
    this.select(p.host)
  }

  private nearestTerrace(host: string, point: THREE.Vector3): string | null {
    const isl = this.islands.get(host)
    if (!isl) return null
    const local = isl.group.worldToLocal(point.clone())
    let best: string | null = null
    let bd = Infinity
    for (const t of isl.terraces.values()) {
      const d = Math.hypot(local.x - t.spec.x, local.z - t.spec.z)
      if (d < bd) {
        bd = d
        best = t.spec.id
      }
    }
    return best
  }

  private terraceWorld(isl: IslandView, t: TerraceView): THREE.Vector3 {
    return isl.group.localToWorld(new THREE.Vector3(t.spec.x, 0.6, t.spec.z))
  }

  private applyTheme(): void {
    this.sky.applyTheme()
    this.tethers.applyTheme()
  }

  private showRings(): void {
    this.citadel.ring.visible = this.nav.selected === 'cit'
    for (const isl of this.islands.values()) isl.ring.visible = this.nav.selected === isl.id && this.nav.terrace === null
  }

  private resize(): void {
    const w = Math.max(1, this.stage.clientWidth)
    const h = Math.max(1, this.stage.clientHeight)
    this.renderer.setSize(w, h, false)
    this.rig.resize(w, h)
  }

  /** Returns what is under a screen point, or null. */
  private pickAt(clientX: number, clientY: number): Pick | null {
    const rect = this.canvas.getBoundingClientRect()
    this.ndc.set(((clientX - rect.left) / rect.width) * 2 - 1, -((clientY - rect.top) / rect.height) * 2 + 1)
    this.raycaster.setFromCamera(this.ndc, this.rig.camera)
    for (const hit of this.raycaster.intersectObjects(this.pickRoots, true)) {
      // Hidden things still intersect the ray in this three.js, such as the
      // trees a terrace covers, which would hand a building's click to the
      // island. Skip anything not drawn.
      if (!isDrawn(hit.object)) continue
      const res: Pick = { host: '', terrace: null, bld: null, agent: null, point: hit.point }
      let o: THREE.Object3D | null = hit.object
      while (o) {
        const u = o.userData
        if (u.agentId && !res.agent) {
          res.agent = u.agentId as string
          res.terrace = u.terraceId as string
        }
        if (u.bldIdx !== undefined && res.bld === null) {
          res.bld = u.bldIdx as number
          res.terrace = u.terraceId as string
        }
        if (u.terraceId && !res.terrace) res.terrace = u.terraceId as string
        if (u.host) {
          res.host = u.host as string
          return res
        }
        o = o.parent
      }
    }
    return null
  }

  /** A visible selection turns its ring slowly through drifting hues, and a selected terrace's rope pulses. */
  private animateSelection(dt: number): void {
    const spin = reduceMotion ? 0 : dt * 0.35
    // The rings share one material. A pastel hue sweeps round every fifteen
    // seconds or so, which keeps the ring lively without shouting.
    if (reduceMotion) M.ring.color.set(css('--accent', '#2D5BC7'))
    else M.ring.color.setHSL((this.time * 0.065) % 1, 0.7, 0.72)
    if (this.citadel.ring.visible) this.citadel.ring.rotation.z += spin
    const n = this.nav
    // The selected building breathes. Any other state it had, such as an
    // agent reading it, comes back the moment it is deselected.
    const focused = n.file !== null && n.terrace ? this.islands.get(n.selected) : undefined
    const b = (focused && n.terrace ? focused.terraces.get(n.terrace)?.meshes.blds[n.file as number] : undefined) ?? null
    if (this.selectedBld && this.selectedBld !== b) {
      this.selectedBld.material = M.bld
      refreshBuilding(this.selectedBld)
    }
    this.selectedBld = b
    if (b) {
      if (b.material !== M.bldSelected) b.material = M.bldSelected
      M.bldSelected.emissiveIntensity = reduceMotion ? 0.55 : 0.4 + 0.35 * Math.sin(this.time * 2.6)
    }
    for (const isl of this.islands.values()) {
      if (isl.ring.visible) isl.ring.rotation.z += spin
      for (const t of isl.terraces.values()) {
        const selected = isl.id === n.selected && t.spec.id === n.terrace
        const glow = selected ? (reduceMotion ? 0.7 : 0.45 + 0.4 * Math.sin(this.time * 2.6)) : ROPE_GLOW
        if (t.meshes.rope.emissiveIntensity !== glow) t.meshes.rope.emissiveIntensity = glow
      }
    }
  }

  /** Moves each island toward its place, out and down while unreachable, and lights its crystal. */
  private updateIslands(dt: number): void {
    const k = reduceMotion ? 1 : Math.min(1, dt * 1.6)
    for (const isl of this.islands.values()) {
      const h = isl.def
      const up = isl.reachable
      isl.drift += ((up ? 0 : 1) - isl.drift) * k
      const d = isl.drift
      const dist = h.dist + 12 * d
      const y = h.y - 3 * d
      const bob = reduceMotion ? 0 : Math.sin(this.time * 0.6 + h.phase) * 0.3
      isl.pos.set(Math.cos(h.angle) * dist, y + bob, Math.sin(h.angle) * dist)
      isl.group.position.copy(isl.pos)
      isl.group.rotation.y = -h.angle + Math.PI / 2
      // the crystal is the daemon as the citadel last heard it: lit while reachable, grey while it can't tell
      isl.core.mat.emissiveIntensity = up ? 1.1 : 0
      isl.core.mat.color.set(up ? 0x7fe0f0 : 0x4a5866)
      isl.core.light.intensity = up ? 1.3 : 0
      isl.core.mesh.rotation.y += dt * (up && !this.stale ? 0.8 : 0)
    }
  }

  private updateTethers(): void {
    const end = this.citadel.group.localToWorld(this.citadel.core.local.clone())
    for (const isl of this.islands.values()) {
      const start = isl.group.localToWorld(isl.core.local.clone())
      const link = this.stale ? 'unknown' : isl.reachable ? 'up' : 'down'
      this.tethers.update(isl.id, { start, drift: isl.drift, link }, end, this.time, this.frameNo)
    }
  }

  /**
   * Keeps the camera's target on whatever is selected. Islands glide in,
   * drift, and bob, so a target taken once would be left behind.
   */
  private trackTarget(): void {
    const n = this.nav
    const isl = this.islands.get(n.selected)
    if (!isl) return
    const t = n.terrace ? isl.terraces.get(n.terrace) : undefined
    const b = t && n.file !== null ? t.meshes.blds[n.file] : undefined
    if (n.agent && this.agents.position(n.agent, this.rig.want.target)) return
    if (b) this.rig.want.target.copy(bldWorld(b))
    else if (t) this.rig.want.target.copy(this.terraceWorld(isl, t))
    else this.rig.want.target.set(isl.pos.x, isl.pos.y + 1.5, isl.pos.z)
  }

  private placeLabels(): void {
    const n = this.nav
    const deep = n.terrace !== null
    this.labels.placeAt(this.citLabel, tmpW.set(0, this.citadel.group.position.y + this.citadel.labelY, 0), 0)
    for (const isl of this.islands.values()) {
      if (deep && isl.id === n.selected) this.labels.hide(isl.label)
      else this.labels.placeAt(isl.label, isl.pos, 5.5)
    }
    this.agents.placeTags(deep ? { host: n.selected, terrace: n.terrace as string } : null, n.agent, n.file)
  }

  private frame(now: number): void {
    const dt = Math.min(0.05, Math.max(0, (now - this.last) / 1000))
    this.last = now
    this.time += dt
    this.frameNo++

    this.updateIslands(dt)
    this.updateTethers()
    this.agents.update(dt, this.time)
    this.trackTarget()
    this.animateSelection(dt)

    const cit = this.citadel
    // The citadel's crystal is the world's NATS server as the citadel reports it.
    const known = this.crystalLit && !this.stale
    cit.core.mat.emissiveIntensity = known ? 1.1 : 0
    cit.core.mat.color.set(known ? 0x7fe0f0 : 0x4a5866)
    cit.core.light.intensity = known ? 1.3 : 0
    cit.core.mesh.rotation.y += dt * (known ? 0.5 : 0)
    const cycle = (this.time * 0.8) % 1
    cit.halo.visible = this.beaconOn && known
    cit.halo.scale.setScalar(reduceMotion ? 1.6 : (1 + 0.6 * cycle) * 1.6)
    cit.halo.material.opacity = reduceMotion ? 0.3 : 0.45 * (1 - cycle)
    cit.light.intensity = this.beaconOn ? 1.1 : 0.4
    if (!reduceMotion) this.sky.puffs.rotation.y += dt * 0.01

    this.rig.update(reduceMotion ? 1 : Math.min(1, dt * 3))
    this.placeLabels()
    this.renderer.render(this.scene, this.rig.camera)
    requestAnimationFrame((n) => this.frame(n))
  }
}

