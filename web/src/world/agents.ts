import * as THREE from 'three'
import { el, reduceMotion } from '../dom'
import { base } from '../files'
import type { Airship, Provider } from './airship'
import type { Labels } from './labels'
import { G, M } from './materials'
import { disposeTree, type BuildingMats } from './theme'

/** What the page knows about one running or just-finished agent. */
export interface AgentState {
  id: string
  terrace: string
  prov: Provider
  name: string
  /** What the agent reports it is doing, for the name tag when it is not flying. */
  action: string
  status: 'running' | 'idle' | 'ended' | 'failed'
  /** Idle with a last message nobody has seen yet, or a tool call waiting on a person. */
  waiting: boolean
  /** A tool call is waiting on a person: the airship shows a beam. */
  approval: boolean
  /** The file it is on, repo-relative, or null before the first tool call. */
  file: string | null
  kind: 'read' | 'edit'
  /** Whether the latest tool call touched that file. Otherwise the agent is on the roof doing something else. */
  atFile: boolean
  /** Context fill, 0 to 100. */
  ctx: number
}

/** The terrace an agent works on, as the world exposes it. */
export interface TerraceAccess {
  /** The island group the terrace belongs to; airships are its children. */
  island: THREE.Group
  blds: THREE.Mesh[]
  /** Building index by repo-relative path, for the files that have one. */
  pathIdx: Map<string, number>
  center: { x: number; z: number }
}

/** A place an airship sits or hovers: a building's roof, or the terrace's pad. */
interface Spot {
  x: number
  z: number
  top: number
  idx: number | null
}

interface AgentView {
  id: string
  host: string
  terrace: string
  prov: Provider
  mesh: THREE.Group
  mats: THREE.Material[]
  bags: THREE.Mesh[]
  /** Moves what the airship's theme animates on it. */
  animate?: (dt: number) => void
  tag: HTMLButtonElement
  name: string
  action: string
  status: AgentState['status']
  waiting: boolean
  approval: boolean
  beam: THREE.Mesh
  file: string | null
  kind: 'read' | 'edit'
  atFile: boolean
  ctx: number
  from: Spot
  to: Spot
  phase: 'dwell' | 'move' | 'leave'
  t: number
  bob: number
  /** The building this agent is lighting, while landed. */
  dwellIdx: number | null
}

const MOVE = 3.0
const LAND = 0.74
const LEAVE = 2.5
const FILE_TAGS = 12

/** Flight altitude: a fuller airship flies lower. */
function altFor(ctx: number): number {
  return 3.4 - 2.4 * Math.min(1, Math.max(0, ctx / 100))
}

function easeIO(x: number): number {
  return 0.5 - Math.cos(Math.PI * Math.min(1, Math.max(0, x))) / 2
}

const tmp = new THREE.Vector3()

/** Every airship in the world, and the buildings they light. */
export class Agents {
  private readonly views = new Map<string, AgentView>()
  private readonly fileTags: HTMLSpanElement[] = []
  /** Called when an airship or its tag is clicked. */
  onPick: (host: string, terrace: string, id: string) => void = () => {}

  constructor(
    private readonly labelsEl: HTMLElement,
    private readonly labels: Labels,
    private readonly terraceOf: (host: string, terrace: string) => TerraceAccess | undefined,
    /** Builds an airship in the world's current theme. */
    private readonly airship: (prov: Provider) => Airship,
  ) {
    for (let i = 0; i < FILE_TAGS; i++) {
      const tag = el('span', { class: 'ftag', 'aria-hidden': 'true' })
      labelsEl.appendChild(tag)
      this.fileTags.push(tag)
    }
  }

  /** Reconciles one host's airships with what the citadel reports. */
  set(host: string, states: AgentState[]): void {
    const seen = new Set<string>()
    for (const s of states) {
      seen.add(s.id)
      let v = this.views.get(s.id)
      if (!v) {
        // Finished sessions get no airship; a page opened later shows the terrace at rest.
        if (s.status !== 'running' && s.status !== 'idle') continue
        const t = this.terraceOf(host, s.terrace)
        if (!t) continue
        v = this.create(host, s, t)
      }
      this.apply(v, s)
    }
    for (const v of this.views.values()) {
      if (v.host === host && !seen.has(v.id) && v.phase !== 'leave') this.leave(v)
    }
  }

  /** The world position of an agent's airship, for the camera to follow. */
  position(id: string, out: THREE.Vector3): boolean {
    const v = this.views.get(id)
    if (!v) return false
    v.mesh.getWorldPosition(out)
    return true
  }

  /** Buildings were rebuilt, so what agents were lighting is gone. They light it again as they land. */
  terraceRebuilt(host: string, terrace: string): void {
    for (const v of this.views.values()) if (v.host === host && v.terrace === terrace) v.dwellIdx = null
  }

  /** Builds an agent's airship, with the beam it shows while a tool call waits on a person. */
  private ship(id: string, terrace: string, prov: Provider): { ship: Airship; beam: THREE.Mesh } {
    const ship = this.airship(prov)
    ship.group.userData.agentId = id
    ship.group.userData.terraceId = terrace
    // The beam drops from under the hull to whatever is below while a tool
    // call waits on a person. It is not pickable; the airship is.
    const beam = new THREE.Mesh(G.beam, M.beam)
    beam.visible = false
    beam.raycast = () => {}
    ship.group.add(beam)
    return { ship, beam }
  }

  /** Takes every airship off its island, before the islands are rebuilt. */
  detachShips(): void {
    for (const v of this.views.values()) v.mesh.parent?.remove(v.mesh)
  }

  /**
   * Gives every airship a new hull from the current theme, where the old
   * one was, on its island as rebuilt. Buildings were rebuilt too, so what
   * an agent was lighting is lit again as it lands. A ship already leaving
   * is gone.
   */
  rebuildShips(): void {
    for (const v of [...this.views.values()]) {
      const t = this.terraceOf(v.host, v.terrace)
      if (!t || v.phase === 'leave') {
        this.remove(v)
        continue
      }
      const { ship, beam } = this.ship(v.id, v.terrace, v.prov)
      ship.group.position.copy(v.mesh.position)
      ship.group.rotation.copy(v.mesh.rotation)
      disposeTree(v.mesh)
      t.island.add(ship.group)
      v.mesh = ship.group
      v.mats = ship.mats
      v.bags = ship.bags
      v.animate = ship.animate
      v.beam = beam
      v.dwellIdx = null
      if (v.to.idx !== null) v.to = t.blds[v.to.idx] ? bldSpot(t, v.to.idx) : padSpot(t)
    }
  }

  private create(host: string, s: AgentState, t: TerraceAccess): AgentView {
    const { ship, beam } = this.ship(s.id, s.terrace, s.prov)
    t.island.add(ship.group)
    const tag = el('button', { type: 'button', class: 'tag', onclick: () => this.onPick(host, s.terrace, s.id) })
    this.labelsEl.appendChild(tag)
    const pad = padSpot(t)
    const v: AgentView = {
      id: s.id, host, terrace: s.terrace, prov: s.prov, mesh: ship.group, mats: ship.mats, bags: ship.bags, animate: ship.animate, tag,
      name: s.name, action: s.action, status: s.status, waiting: s.waiting, approval: s.approval, beam, file: s.file, kind: s.kind, atFile: s.atFile, ctx: s.ctx,
      from: pad, to: pad, phase: 'dwell', t: 0, bob: Math.random() * 6.283, dwellIdx: null,
    }
    ship.group.position.set(pad.x, pad.top + altFor(s.ctx), pad.z)
    this.views.set(s.id, v)
    return v
  }

  private apply(v: AgentView, s: AgentState): void {
    v.name = s.name
    v.action = s.action
    v.status = s.status
    v.waiting = s.waiting
    v.approval = s.approval
    v.ctx = s.ctx
    v.kind = s.kind
    v.file = s.file
    v.atFile = s.atFile
    if (s.status !== 'running' && s.status !== 'idle') {
      if (v.phase !== 'leave') this.leave(v)
      return
    }
    const t = this.terraceOf(v.host, v.terrace)
    if (!t) return
    const idx = s.file !== null ? (t.pathIdx.get(s.file) ?? null) : null
    const spot = idx !== null ? bldSpot(t, idx) : padSpot(t)
    if (spot.idx !== v.to.idx) this.startMove(v, t, spot)
    else if (v.phase === 'dwell' && v.dwellIdx !== null && v.dwellIdx === spot.idx) {
      // Same building, maybe a different kind of touch.
      setDwell(t, v.dwellIdx, v.id, v.kind)
    }
  }

  /** Takeoff: leave the building and head for the next spot. */
  private startMove(v: AgentView, t: TerraceAccess, to: Spot): void {
    if (v.dwellIdx !== null) {
      clearDwell(t, v.dwellIdx, v.id)
      v.dwellIdx = null
    }
    v.from = { x: v.mesh.position.x, z: v.mesh.position.z, top: v.mesh.position.y - LAND, idx: v.to.idx }
    v.to = to
    v.phase = 'move'
    v.t = 0
  }

  /** The session ended: let go of the building, lift away, and fade. */
  private leave(v: AgentView): void {
    const t = this.terraceOf(v.host, v.terrace)
    if (t && v.dwellIdx !== null) clearDwell(t, v.dwellIdx, v.id)
    v.dwellIdx = null
    v.phase = 'leave'
    v.t = 0
    for (const m of v.mats) m.transparent = true
    this.labels.hide(v.tag)
  }

  private remove(v: AgentView): void {
    disposeTree(v.mesh)
    v.tag.remove()
    this.views.delete(v.id)
  }

  /** Moves every airship one frame. */
  update(dt: number, time: number): void {
    for (const v of [...this.views.values()]) {
      v.animate?.(dt)
      const nb = Math.min(4, Math.ceil(v.ctx / 25))
      v.bags.forEach((b, i) => (b.visible = i < nb))
      const bob = reduceMotion ? 0 : Math.sin(time * 1.8 + v.bob) * 0.1
      v.beam.visible = v.approval && v.phase !== 'leave'
      if (v.beam.visible) {
        // From the gondola down to the roof or the pad, pulsing a little.
        const len = Math.max(0.3, v.mesh.position.y - v.to.top - 0.6)
        v.beam.scale.set(1, len, 1)
        v.beam.position.y = -0.6 - len / 2
        ;(v.beam.material as THREE.MeshBasicMaterial).opacity = reduceMotion ? 0.85 : 0.6 + 0.3 * Math.sin(time * 4)
      }

      if (v.phase === 'leave') {
        v.t += dt
        v.mesh.position.y += dt * 2.2
        const opacity = Math.max(0, 1 - v.t / LEAVE)
        for (const m of v.mats) m.opacity = opacity
        if (v.t > LEAVE || reduceMotion) this.remove(v)
        continue
      }

      const t = this.terraceOf(v.host, v.terrace)
      if (!t) continue
      if (v.phase === 'move') {
        v.t += dt
        if (v.t > MOVE || reduceMotion) {
          v.phase = 'dwell'
          v.t = 0
        }
      }
      if (v.phase === 'dwell') {
        if (v.to.idx !== null && v.dwellIdx !== v.to.idx) {
          setDwell(t, v.to.idx, v.id, v.kind)
          v.dwellIdx = v.to.idx
        }
        if (v.to.idx !== null) {
          // Landed: sit on the roof, side by side when the roof is shared.
          const b = t.blds[v.to.idx]
          const ids = b ? Object.keys(b.userData.dwell as Record<string, string>) : []
          const k = ids.indexOf(v.id)
          const off = ids.length > 1 && k >= 0 ? (k - (ids.length - 1) / 2) * 0.6 : 0
          v.mesh.position.set(v.to.x + off, v.to.top + LAND, v.to.z)
        } else {
          // Nothing to land on yet: circle slowly over the pad, so an active agent never looks parked.
          const a = reduceMotion ? v.bob : time * 0.45 + v.bob
          v.mesh.position.set(v.to.x + Math.cos(a) * 1.2, v.to.top + altFor(v.ctx) + bob, v.to.z + Math.sin(a) * 1.2)
          v.mesh.rotation.y = -a - Math.PI / 2
        }
        continue
      }

      const p = Math.min(1, v.t / MOVE)
      const A = v.from
      const B = v.to
      const roofA = A.top + LAND
      const roofB = B.idx !== null ? B.top + LAND : B.top + altFor(v.ctx)
      const cruise = Math.max(A.top, B.top) + altFor(v.ctx)
      let y: number
      if (p < 0.25) y = roofA + (cruise - roofA) * easeIO(p / 0.25)
      else if (p > 0.75) y = cruise + (roofB - cruise) * easeIO((p - 0.75) / 0.25)
      else y = cruise
      const h = p < 0.2 ? 0 : p > 0.8 ? 1 : easeIO((p - 0.2) / 0.6)
      v.mesh.position.set(A.x + (B.x - A.x) * h, y + (p > 0.2 && p < 0.8 ? bob : 0), A.z + (B.z - A.z) * h)
      const dx = B.x - A.x
      const dz = B.z - A.z
      if (Math.abs(dx) + Math.abs(dz) > 0.01) v.mesh.rotation.y = Math.atan2(-dz, dx)
    }
  }

  /** Places name tags on the terrace being viewed, and file tags on busy buildings. */
  placeTags(deep: { host: string; terrace: string } | null, selectedAgent: string | null, focusedIdx: number | null): void {
    for (const v of this.views.values()) {
      const here = deep && v.host === deep.host && v.terrace === deep.terrace && v.phase !== 'leave'
      if (!here) {
        this.labels.hide(v.tag)
        continue
      }
      const now = this.now(v)
      const text = `${v.name}\u0000${now}`
      if (v.tag.dataset.text !== text) {
        v.tag.dataset.text = text
        v.tag.textContent = ''
        v.tag.appendChild(el('b', { text: v.name }))
        v.tag.appendChild(el('span', { text: now }))
        v.tag.setAttribute('aria-label', `${v.name}, ${now}`)
      }
      const cls = `tag ${v.waiting ? 'waiting' : 'working'}${selectedAgent === v.id ? ' sel' : ''}`
      if (v.tag.className !== cls) v.tag.className = cls
      v.mesh.getWorldPosition(tmp)
      this.labels.placeAt(v.tag, tmp, 1.0)
    }

    let used = 0
    const t = deep ? this.terraceOf(deep.host, deep.terrace) : undefined
    if (t) {
      t.blds.forEach((b, bi) => {
        const dwell = b.userData.dwell as Record<string, string>
        const n = Object.keys(dwell).length
        if ((!n && bi !== focusedIdx) || used >= this.fileTags.length) return
        const node = this.fileTags[used++]
        const mats = b.userData.mats as BuildingMats
        const cls = b.material === mats.conflict ? 'conflict' : b.material === mats.edit ? 'edit' : b.material === mats.read ? 'read' : ''
        const txt = base(b.userData.file as string) + (cls === 'conflict' ? `, ${n} sessions` : '')
        if (node.textContent !== txt) node.textContent = txt
        const cn = `ftag ${cls}`
        if (node.className !== cn) node.className = cn
        this.labels.placeAt(node, bldWorld(b), 0, true)
      })
    }
    for (let i = used; i < this.fileTags.length; i++) this.labels.hide(this.fileTags[i])
  }

  /** What the tag says under the name: the flight, the file, or whatever else it is doing. */
  private now(v: AgentView): string {
    if (v.approval) return 'Needs your answer'
    if (v.status === 'idle' && v.phase !== 'move') return v.waiting ? 'Waiting for you' : 'Idle'
    const f = v.file ? base(v.file) : ''
    if (v.phase === 'move') {
      // A file without a building is worked on from the air over the terrace.
      if (v.to.idx === null) return f ? `${v.kind === 'edit' ? 'Editing' : 'Reading'} ${f}` : 'Returning to the terrace'
      const p = v.t / MOVE
      return `${p < 0.25 ? 'Taking off for' : p > 0.75 ? 'Landing on' : 'Flying to'} ${f}`
    }
    if (f && v.atFile) return `${v.kind === 'edit' ? 'Editing' : 'Reading'} ${f}`
    return v.action || (f ? `On ${f}` : 'Starting')
  }
}

function padSpot(t: TerraceAccess): Spot {
  return { x: t.center.x, z: t.center.z, top: 0.35, idx: null }
}

/** A building's roof, on the island. Buildings stand where they are from their terrace's center. */
function bldSpot(t: TerraceAccess, idx: number): Spot {
  const b = t.blds[idx]
  return { x: t.center.x + b.position.x, z: t.center.z + b.position.z, top: b.userData.top as number, idx }
}

/** The world position of a building's roof. */
export function bldWorld(b: THREE.Mesh): THREE.Vector3 {
  return (b.parent as THREE.Object3D).localToWorld(new THREE.Vector3(b.position.x, b.userData.top as number, b.position.z))
}

function setDwell(t: TerraceAccess, idx: number, agentId: string, kind: string): void {
  const b = t.blds[idx]
  if (!b) return
  ;(b.userData.dwell as Record<string, string>)[agentId] = kind
  refreshBuilding(b)
}

function clearDwell(t: TerraceAccess, idx: number, agentId: string): void {
  const b = t.blds[idx]
  if (!b) return
  delete (b.userData.dwell as Record<string, string>)[agentId]
  refreshBuilding(b)
}

/** Amber while an agent edits, blue while one reads, red when two sessions are on the file at once, in the building's theme. */
export function refreshBuilding(b: THREE.Mesh): void {
  const mats = b.userData.mats as BuildingMats
  // A selected building keeps its pulse; the world restores it on deselect.
  if (b.material === mats.selected) return
  const dwell = b.userData.dwell as Record<string, string>
  const ids = Object.keys(dwell)
  if (!ids.length) {
    b.material = mats.rest
    return
  }
  const edit = ids.some((id) => dwell[id] === 'edit')
  b.material = ids.length > 1 ? mats.conflict : edit ? mats.edit : mats.read
}
