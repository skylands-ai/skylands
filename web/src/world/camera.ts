import * as THREE from 'three'

export interface Pose {
  target: THREE.Vector3
  radius: number
  theta: number
  phi: number
}

/** The opening view: the whole world with the citadel at center. */
export const HOME: Pose = { target: new THREE.Vector3(0, 0, 0), radius: 125, theta: 1.55, phi: 1.12 }

const MIN_RADIUS = 5
const MAX_RADIUS = 220

function nearestAngle(from: number, to: number): number {
  let d = to - from
  while (d > Math.PI) d -= Math.PI * 2
  while (d < -Math.PI) d += Math.PI * 2
  return from + d
}

/** An orbit camera that glides toward where it is asked to look. */
export class CameraRig {
  readonly camera: THREE.PerspectiveCamera
  private readonly cam: Pose = { target: HOME.target.clone(), radius: HOME.radius, theta: HOME.theta, phi: HOME.phi }
  readonly want: Pose = { target: HOME.target.clone(), radius: HOME.radius, theta: HOME.theta, phi: HOME.phi }

  constructor() {
    this.camera = new THREE.PerspectiveCamera(45, 1, 0.1, 1400)
  }

  resize(w: number, h: number): void {
    this.camera.aspect = w / h
    this.camera.updateProjectionMatrix()
  }

  /** Glides to look at target from radius away. A null theta keeps the current heading. */
  flyTo(target: THREE.Vector3, radius: number, theta: number | null, phi: number): void {
    this.want.target.copy(target)
    this.want.radius = radius
    if (theta !== null) this.want.theta = nearestAngle(this.cam.theta, theta)
    this.want.phi = phi
  }

  orbitBy(dx: number, dy: number): void {
    this.want.theta += dx * 0.006
    this.want.phi = Math.min(1.45, Math.max(0.3, this.want.phi - dy * 0.005))
  }

  zoomTo(radius: number): void {
    this.want.radius = Math.min(MAX_RADIUS, Math.max(MIN_RADIUS, radius))
  }

  /** Moves a fraction k of the way toward the wanted pose and points the camera. */
  update(k: number): void {
    const cam = this.cam
    const want = this.want
    cam.target.lerp(want.target, k)
    cam.radius += (want.radius - cam.radius) * k
    cam.theta += (want.theta - cam.theta) * k
    cam.phi += (want.phi - cam.phi) * k
    const sp = Math.sin(cam.phi)
    this.camera.position.set(
      cam.target.x + cam.radius * sp * Math.cos(cam.theta),
      cam.target.y + cam.radius * Math.cos(cam.phi),
      cam.target.z + cam.radius * sp * Math.sin(cam.theta),
    )
    this.camera.lookAt(cam.target)
  }
}
