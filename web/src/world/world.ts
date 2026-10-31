import * as THREE from 'three'
import { el, reduceMotion } from '../dom'
import { CameraRig, HOME } from './camera'
import { buildCitadel, type Citadel } from './citadel'
import { attachPointerInput } from './input'
import { buildIsland } from './island'
import { Labels } from './labels'
import { placeIsland, type Placement } from './placement'
import type { Crystal } from './shapes'
import { Sky } from './sky'
import { Tethers } from './tethers'

/** Text and a CSS color for a status dot. */
export type Status = [text: string, color: string]

interface IslandView {
  id: string
  def: Placement
  group: THREE.Group
  core: Crystal
  ring: THREE.Mesh
  pos: THREE.Vector3
  /** 0 while reachable, easing to 1 while the citadel can't reach the host. */
  drift: number
  reachable: boolean
  label: HTMLButtonElement
}

const tmpW = new THREE.Vector3()

/**
 * The 3D world: renderer, scene, camera, the citadel, the islands, and the
 * frame loop. It knows nothing about NATS or daemons. The page tells it
 * which islands exist and whether each is reachable.
 */
export class World {
  readonly citadel: Citadel
  /** What is selected: 'cit' or a host id. */
  selected = 'cit'
  /** Called when a click in the world or on a label selects something. */
  onSelect: (id: string) => void = () => {}

  private readonly renderer: THREE.WebGLRenderer
  private readonly scene = new THREE.Scene()
  private readonly rig = new CameraRig()
  private readonly sky: Sky
  private readonly tethers: Tethers
  private readonly labels: Labels
  private readonly citLabel: HTMLButtonElement
  private readonly islands = new Map<string, IslandView>()
  private readonly pickRoots: THREE.Object3D[] = []
  private readonly raycaster = new THREE.Raycaster()
  private readonly ndc = new THREE.Vector2()
  private beaconOn = false
  /** True while the page can't reach the citadel: nothing in the scene is current. */
  private stale = false
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

    if (window.ResizeObserver) new ResizeObserver(() => this.resize()).observe(stage)
    window.addEventListener('resize', () => this.resize())
    this.resize()

    this.applyTheme()
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    mq?.addEventListener('change', () => this.applyTheme())
    new MutationObserver(() => this.applyTheme()).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] })

    attachPointerInput(canvas, this.rig, {
      onPick: (x, y) => {
        const id = this.pickAt(x, y)
        if (id) this.select(id)
      },
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
      isl = { id, def, group: m.group, core: m.core, ring: m.ring, pos: new THREE.Vector3(), drift: 1, reachable, label: this.labels.create(() => this.select(id)) }
      this.islands.set(id, isl)
      this.updateIslands(0)
    }
    isl.reachable = reachable
  }

  /** Fills the floating label of the citadel or an island. */
  setLabel(id: string, name: string, status: Status, waiting: number): void {
    const b = id === 'cit' ? this.citLabel : this.islands.get(id)?.label
    if (!b) return
    b.textContent = ''
    b.appendChild(el('span', { class: 'nm' }, [name, waiting ? el('span', { class: 'badge', text: String(waiting) }) : null]))
    b.appendChild(el('span', { class: 'st' }, [el('span', { class: 'dot', style: `background:${status[1]}` }), status[0]]))
    b.setAttribute('aria-pressed', this.selected === id ? 'true' : 'false')
    b.setAttribute('aria-label', `${name}, ${status[0]}${waiting ? `, ${waiting} waiting on you` : ''}. Opens the ${id === 'cit' ? 'citadel' : 'island'}.`)
  }

  /** Lights the beacon on the top tower while anything waits on a person. */
  setBeacon(on: boolean): void {
    this.beaconOn = on
  }

  /** Marks the whole scene as the last state heard: the citadel's crystal goes grey and nothing flows. */
  setStale(on: boolean): void {
    this.stale = on
  }

  /** Flies to the whole-world view. */
  overview(): void {
    this.selected = 'cit'
    this.rig.flyTo(HOME.target, HOME.radius, HOME.theta, HOME.phi)
    this.showRings()
    this.onSelect('cit')
  }

  select(id: string): void {
    const isl = this.islands.get(id)
    if (id !== 'cit' && !isl) return
    this.selected = id
    if (isl) {
      const h = isl.def
      const p = isl.pos
      this.rig.flyTo(new THREE.Vector3(p.x, p.y + 1.5, p.z), h.R * 2.6 + 16, h.angle, 1.0)
    } else this.rig.flyTo(new THREE.Vector3(0, 10, 0), 52, null, 0.95)
    this.showRings()
    this.onSelect(id)
  }

  /** Esc: one level up. From an island that is the whole world. */
  up(): void {
    if (this.selected !== 'cit') this.overview()
  }

  start(): void {
    requestAnimationFrame((now) => this.frame(now))
  }

  private applyTheme(): void {
    this.sky.applyTheme()
    this.tethers.applyTheme()
  }

  private showRings(): void {
    this.citadel.ring.visible = this.selected === 'cit'
    for (const isl of this.islands.values()) isl.ring.visible = this.selected === isl.id
  }

  private resize(): void {
    const w = Math.max(1, this.stage.clientWidth)
    const h = Math.max(1, this.stage.clientHeight)
    this.renderer.setSize(w, h, false)
    this.rig.resize(w, h)
  }

  /** Returns the id of the thing under a screen point, or null. */
  private pickAt(clientX: number, clientY: number): string | null {
    const rect = this.canvas.getBoundingClientRect()
    this.ndc.set(((clientX - rect.left) / rect.width) * 2 - 1, -((clientY - rect.top) / rect.height) * 2 + 1)
    this.raycaster.setFromCamera(this.ndc, this.rig.camera)
    for (const hit of this.raycaster.intersectObjects(this.pickRoots, true)) {
      let o: THREE.Object3D | null = hit.object
      while (o) {
        if (o.userData.host) return o.userData.host as string
        o = o.parent
      }
    }
    return null
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

  private frame(now: number): void {
    const dt = Math.min(0.05, Math.max(0, (now - this.last) / 1000))
    this.last = now
    this.time += dt
    this.frameNo++

    this.updateIslands(dt)
    this.updateTethers()

    const cit = this.citadel
    // The citadel's crystal is the world's NATS server as this page last heard of it.
    const known = !this.stale
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
    this.labels.placeAt(this.citLabel, tmpW.set(0, cit.group.position.y + cit.labelY, 0), 0)
    for (const isl of this.islands.values()) this.labels.placeAt(isl.label, isl.pos, 5.5)
    this.renderer.render(this.scene, this.rig.camera)
    requestAnimationFrame((n) => this.frame(n))
  }
}
