import * as THREE from 'three'

export interface Pose {
  target: THREE.Vector3
  /**
   * How far the view has been dragged off the target. The target follows
   * whatever is selected, so the drag is kept apart from it.
   */
  pan: THREE.Vector3
  radius: number
  theta: number
  phi: number
}

/** The opening view: the whole world with the citadel at center. */
export const HOME: Pose = { target: new THREE.Vector3(0, 0, 0), pan: new THREE.Vector3(), radius: 125, theta: 1.55, phi: 1.12 }

const MIN_RADIUS = 5
const MAX_RADIUS = 220
/** Panning stops this far from the citadel, a little past the outermost island, so the world is never far off screen. */
const MAX_REACH = 75

const homePose = (): Pose => ({ target: HOME.target.clone(), pan: HOME.pan.clone(), radius: HOME.radius, theta: HOME.theta, phi: HOME.phi })

const look = new THREE.Vector3()
const right = new THREE.Vector3()
const up = new THREE.Vector3()

function nearestAngle(from: number, to: number): number {
  let d = to - from
  while (d > Math.PI) d -= Math.PI * 2
  while (d < -Math.PI) d += Math.PI * 2
  return from + d
}

/** An orbit camera that glides toward where it is asked to look. */
export class CameraRig {
  readonly camera: THREE.PerspectiveCamera
  private readonly cam: Pose = homePose()
  readonly want: Pose = homePose()
  private height = 1

  constructor() {
    this.camera = new THREE.PerspectiveCamera(45, 1, 0.1, 1400)
  }

  resize(w: number, h: number): void {
    this.height = h
    this.camera.aspect = w / h
    this.camera.updateProjectionMatrix()
  }

  /** Glides to look at target from radius away, dropping any pan. A null theta keeps the current heading. */
  flyTo(target: THREE.Vector3, radius: number, theta: number | null, phi: number): void {
    this.want.target.copy(target)
    this.want.pan.set(0, 0, 0)
    this.want.radius = radius
    if (theta !== null) this.want.theta = nearestAngle(this.cam.theta, theta)
    this.want.phi = phi
  }

  orbitBy(dx: number, dy: number): void {
    this.want.theta += dx * 0.006
    this.want.phi = Math.min(1.45, Math.max(0.3, this.want.phi - dy * 0.005))
  }

  /** Slides the view by a drag in pixels, so what was under the pointer stays under it. */
  panBy(dx: number, dy: number): void {
    // World units per pixel at the distance of the point being looked at.
    const per = (2 * this.cam.radius * Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2))) / this.height
    right.set(1, 0, 0).applyQuaternion(this.camera.quaternion)
    up.set(0, 1, 0).applyQuaternion(this.camera.quaternion)
    const want = this.want
    want.pan.addScaledVector(right, -dx * per).addScaledVector(up, dy * per)
    look.copy(want.target).add(want.pan)
    if (look.length() > MAX_REACH) want.pan.copy(look.setLength(MAX_REACH)).sub(want.target)
  }

  zoomTo(radius: number): void {
    this.want.radius = Math.min(MAX_RADIUS, Math.max(MIN_RADIUS, radius))
  }

  /** Moves a fraction k of the way toward the wanted pose and points the camera. */
  update(k: number): void {
    const cam = this.cam
    const want = this.want
    cam.target.lerp(want.target, k)
    cam.pan.lerp(want.pan, k)
    cam.radius += (want.radius - cam.radius) * k
    cam.theta += (want.theta - cam.theta) * k
    cam.phi += (want.phi - cam.phi) * k
    look.copy(cam.target).add(cam.pan)
    const sp = Math.sin(cam.phi)
    this.camera.position.set(
      look.x + cam.radius * sp * Math.cos(cam.theta),
      look.y + cam.radius * Math.cos(cam.phi),
      look.z + cam.radius * sp * Math.sin(cam.theta),
    )
    this.camera.lookAt(look)
  }
}
