import * as THREE from 'three'
import { css, reduceMotion } from '../dom'

const SPARKS = 26
const tmp = new THREE.Vector3()

interface Tether {
  tube: THREE.Mesh
  pts: THREE.Points
  mat: THREE.PointsMaterial
  key: string
}

/**
 * How a tether looks. "up" is a lit line with sparks, "down" the slack line of
 * a host the citadel can't reach, and "unknown" a dim line while this page
 * can't reach the citadel, so nothing about the link is current.
 */
export type LinkLook = 'up' | 'down' | 'unknown'

/** What a tether needs to know about its island each frame. */
export interface TetherEnd {
  /** The island's crystal in world space. */
  start: THREE.Vector3
  /** 0 while reachable, easing to 1 as the island drifts out on a slack line. */
  drift: number
  link: LinkLook
}

function makeSparkTexture(): THREE.Texture | null {
  const c = document.createElement('canvas')
  c.width = c.height = 64
  const ctx = c.getContext('2d')
  if (!ctx) return null
  const gr = ctx.createRadialGradient(32, 32, 0, 32, 32, 32)
  gr.addColorStop(0, 'rgba(255,255,255,1)')
  gr.addColorStop(0.4, 'rgba(255,255,255,0.8)')
  gr.addColorStop(1, 'rgba(255,255,255,0)')
  ctx.fillStyle = gr
  ctx.fillRect(0, 0, 64, 64)
  return new THREE.CanvasTexture(c)
}

function tubeAlong(curve: THREE.Curve<THREE.Vector3>, radius: number): THREE.BufferGeometry {
  return new THREE.TubeGeometry(curve, 36, radius, 6, false)
}

/** Each island's leaf node connection to NATS, drawn as a line to the citadel's crystal with sparks for events. */
export class Tethers {
  private readonly tetherMat = new THREE.MeshBasicMaterial({ color: 0x6a8fb8, transparent: true, opacity: 0.85 })
  private readonly slackMat = new THREE.MeshBasicMaterial({ color: 0x8a9bb0, transparent: true, opacity: 0.45 })
  private readonly unknownMat = new THREE.MeshBasicMaterial({ color: 0x6b7f92, transparent: true, opacity: 0.22 })
  private readonly sparkTex = makeSparkTexture()
  private readonly byId = new Map<string, Tether>()

  constructor(private readonly scene: THREE.Scene) {}

  add(id: string): void {
    const tube = new THREE.Mesh(new THREE.BufferGeometry(), this.tetherMat)
    this.scene.add(tube)
    const pg = new THREE.BufferGeometry()
    pg.setAttribute('position', new THREE.Float32BufferAttribute(new Float32Array(SPARKS * 3), 3))
    const mat = new THREE.PointsMaterial({ color: 0xa9e8f5, size: 0.9, transparent: true, depthWrite: false, map: this.sparkTex })
    const pts = new THREE.Points(pg, mat)
    pts.frustumCulled = false
    this.scene.add(pts)
    this.byId.set(id, { tube, pts, mat, key: '' })
    this.applyColorScheme()
  }

  /** Redraws one tether from its island to end, the citadel's crystal. */
  update(id: string, isl: TetherEnd, end: THREE.Vector3, time: number, frameNo: number): void {
    const te = this.byId.get(id)
    if (!te) return
    const mid = isl.start.clone().add(end).multiplyScalar(0.5)
    const sag = 4 + 8 * isl.drift
    mid.y -= sag
    const curve = new THREE.QuadraticBezierCurve3(isl.start, mid, end)
    const key = [isl.start.x, isl.start.y, isl.start.z, sag].map((v) => v.toFixed(1)).join(',')
    if (key !== te.key && frameNo % 2 === 0) {
      te.key = key
      te.tube.geometry.dispose()
      te.tube.geometry = tubeAlong(curve, 0.12)
    }
    te.tube.material = isl.link === 'up' ? this.tetherMat : isl.link === 'down' ? this.slackMat : this.unknownMat
    const n = isl.link === 'up' ? 10 : 0
    const speed = 0.12
    const arr = te.pts.geometry.attributes.position as THREE.BufferAttribute
    for (let i = 0; i < SPARKS; i++) {
      if (i < n) {
        const u = reduceMotion ? i / n : (time * speed + i / n) % 1
        curve.getPoint(u, tmp)
        arr.setXYZ(i, tmp.x, tmp.y, tmp.z)
      } else arr.setXYZ(i, 0, -9999, 0)
    }
    arr.needsUpdate = true
  }

  /** Re-reads the page's color tokens. */
  applyColorScheme(): void {
    this.tetherMat.color.set(css('--tether', '#5C86B8'))
    this.unknownMat.color.set(css('--idle', '#6B7F92'))
    const additive = css('--spark-add', '0') === '1'
    for (const te of this.byId.values()) {
      te.mat.color.set(css('--spark', '#1FA6C8'))
      te.mat.blending = additive ? THREE.AdditiveBlending : THREE.NormalBlending
      te.mat.needsUpdate = true
    }
  }
}
