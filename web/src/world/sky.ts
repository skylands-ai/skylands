import * as THREE from 'three'
import { css } from '../dom'
import { std } from './materials'
import { rng } from './noise'

/** Sky dome, stars, fog, lights, and clouds. Scenery that carries no data, themed from CSS tokens. */
export class Sky {
  readonly skyMat: THREE.ShaderMaterial
  readonly starMat: THREE.PointsMaterial
  readonly fog: THREE.Fog
  readonly hemi: THREE.HemisphereLight
  readonly sun: THREE.DirectionalLight
  readonly cloudMat: THREE.MeshStandardMaterial
  /** Loose cloud puffs that drift slowly around the world. */
  readonly puffs: THREE.Group

  constructor(scene: THREE.Scene) {
    this.skyMat = new THREE.ShaderMaterial({
      uniforms: { top: { value: new THREE.Color() }, horizon: { value: new THREE.Color() } },
      vertexShader:
        'varying vec3 vP; void main(){ vP = (modelMatrix * vec4(position, 1.0)).xyz; gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }',
      fragmentShader:
        'uniform vec3 top; uniform vec3 horizon; varying vec3 vP; void main(){ float h = normalize(vP).y; float t = clamp(h * 1.7 + 0.1, 0.0, 1.0); gl_FragColor = vec4(mix(horizon, top, t), 1.0); }',
      side: THREE.BackSide,
      depthWrite: false,
      fog: false,
    })
    scene.add(new THREE.Mesh(new THREE.SphereGeometry(700, 32, 16), this.skyMat))
    this.fog = new THREE.Fog(0xffffff, 120, 330)
    scene.fog = this.fog

    const starGeo = new THREE.BufferGeometry()
    {
      const r = rng(5)
      const p: number[] = []
      for (let i = 0; i < 500; i++) {
        const u = r() * 6.283
        const y = 0.08 + r() * 0.9
        const s = Math.sqrt(1 - y * y)
        p.push(Math.cos(u) * s * 650, y * 650, Math.sin(u) * s * 650)
      }
      starGeo.setAttribute('position', new THREE.Float32BufferAttribute(p, 3))
    }
    this.starMat = new THREE.PointsMaterial({ color: 0xffffff, size: 1.6, sizeAttenuation: false, transparent: true, opacity: 0, fog: false, depthWrite: false })
    scene.add(new THREE.Points(starGeo, this.starMat))

    this.hemi = new THREE.HemisphereLight(0xffffff, 0x444444, 0.9)
    scene.add(this.hemi)
    this.sun = new THREE.DirectionalLight(0xffffff, 0.9)
    this.sun.position.set(70, 90, 30)
    scene.add(this.sun)
    scene.add(new THREE.AmbientLight(0xffffff, 0.12))

    // clouds: sparse banks with open sky between them
    this.cloudMat = std(0xffffff, { roughness: 1 })
    const cloudGeo = new THREE.IcosahedronGeometry(1, 1)
    const banks: [number, number, number][] = [[2.2, 33, -22]]
    {
      const r = rng(9)
      for (let c = 0; c < 13; c++) banks.push([r() * 6.283, 45 + Math.sqrt(r()) * 160, -30 + r() * 8])
    }
    const PER_BANK = 4
    const clouds = new THREE.InstancedMesh(cloudGeo, this.cloudMat, banks.length * PER_BANK)
    {
      const r = rng(19)
      const m = new THREE.Matrix4()
      const q = new THREE.Quaternion()
      const s = new THREE.Vector3()
      const p = new THREE.Vector3()
      const up = new THREE.Vector3(0, 1, 0)
      for (let c = 0; c < banks.length; c++) {
        const cx = Math.cos(banks[c][0]) * banks[c][1]
        const cz = Math.sin(banks[c][0]) * banks[c][1]
        const cy = banks[c][2]
        for (let k = 0; k < PER_BANK; k++) {
          const sc = 2.2 + r() * 3.2
          p.set(cx + (r() - 0.5) * 9, cy + (r() - 0.5) * 1.6, cz + (r() - 0.5) * 7)
          s.set(sc * (1.2 + r() * 0.6), sc * 0.55, sc)
          q.setFromAxisAngle(up, r() * 6.283)
          m.compose(p, q, s)
          clouds.setMatrixAt(c * PER_BANK + k, m)
        }
      }
      clouds.instanceMatrix.needsUpdate = true
    }
    scene.add(clouds)

    this.puffs = new THREE.Group()
    {
      const r = rng(17)
      for (let c = 0; c < 4; c++) {
        const a = r() * 6.283
        const d = 60 + r() * 70
        const y = 2 + r() * 16
        for (let k = 0; k < 3; k++) {
          const puff = new THREE.Mesh(cloudGeo, this.cloudMat)
          const sc = 1.6 + r() * 2.2
          puff.scale.set(sc * 1.4, sc * 0.6, sc)
          puff.position.set(Math.cos(a) * d + (r() - 0.5) * 7, y + (r() - 0.5) * 1.5, Math.sin(a) * d + (r() - 0.5) * 5)
          this.puffs.add(puff)
        }
      }
    }
    scene.add(this.puffs)
  }

  /** Re-reads the theme tokens. Day sky in the light theme, dusk in the dark one. */
  applyTheme(): void {
    this.skyMat.uniforms.top.value.set(css('--sky-top', '#5C9BE0'))
    this.skyMat.uniforms.horizon.value.set(css('--sky-horizon', '#D6E9F7'))
    this.fog.color.set(css('--fog', '#D6E9F7'))
    this.cloudMat.color.set(css('--cloud', '#FFFFFF'))
    this.hemi.color.set(css('--hemi-sky', '#D7EAFF'))
    this.hemi.groundColor.set(css('--hemi-ground', '#7C8A66'))
    this.hemi.intensity = parseFloat(css('--hemi-int', '0.95'))
    this.sun.color.set(css('--sun', '#FFF3DF'))
    this.sun.intensity = parseFloat(css('--sun-int', '0.95'))
    this.starMat.opacity = parseFloat(css('--stars', '0'))
  }
}
