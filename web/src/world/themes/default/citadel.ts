import * as THREE from 'three'
import { G, M } from '../../materials'
import { ringNoise } from '../../noise'
import { crenelRing, crenelSquare, crystalAt, slit, underside } from '../../shapes'
import type { Citadel } from '../../theme'
import { selectionRing } from './ring'

/** The castle at the center of the world. It looks the same whether one citadel runs or ten. */
export function buildCitadel(): Citadel {
  const g = new THREE.Group()
  const noise = ringNoise(77)
  const baseMesh = new THREE.Mesh(new THREE.CylinderGeometry(9, 8.7, 1.6, 24), [M.stoneWall, M.stoneTop, M.stoneWall])
  baseMesh.position.y = -0.8
  g.add(baseMesh)
  const under = new THREE.Mesh(underside(8.7, 12, noise, 77, [0x6e6a66, 0x55504b, 0x3b3734]), M.rock)
  under.position.y = -1.6 - 6
  g.add(under)

  // curtain wall: 18 straight segments with a walkway and merlons
  const WR = 7.4
  const WH = 2.4
  const SEG = 18
  for (let s = 0; s < SEG; s++) {
    const a = ((s + 0.5) / SEG) * Math.PI * 2
    const seg = new THREE.Mesh(G.box, M.stoneWall)
    seg.scale.set(2 * WR * Math.sin(Math.PI / SEG) + 0.05, WH, 0.8)
    seg.position.set(Math.cos(a) * WR, WH / 2, Math.sin(a) * WR)
    seg.rotation.y = -a + Math.PI / 2
    g.add(seg)
  }
  crenelRing(g, WR + 0.25, WH, 40, [0.5, 0.55, 0.3])
  // gate facing the opening camera
  const gate = new THREE.Mesh(G.box, M.door)
  gate.scale.set(1.5, 1.7, 0.2)
  const ga = 1.55
  gate.position.set(Math.cos(ga) * (WR + 0.42), 0.85, Math.sin(ga) * (WR + 0.42))
  gate.rotation.y = -ga + Math.PI / 2
  g.add(gate)

  // six flanking towers on the wall
  for (let k = 0; k < 6; k++) {
    const ta = (k / 6) * Math.PI * 2 + 0.26
    const tw = new THREE.Group()
    tw.position.set(Math.cos(ta) * WR, 0, Math.sin(ta) * WR)
    const body = new THREE.Mesh(new THREE.CylinderGeometry(1.15, 1.25, 3.8, 10), [M.stoneKeep, M.stoneTop, M.stoneKeep])
    body.position.y = 1.9
    tw.add(body)
    crenelRing(tw, 1.12, 3.8, 8, [0.38, 0.5, 0.26])
    slit(tw, Math.cos(ta) * 1.22, 2.4, Math.sin(ta) * 1.22, -ta + Math.PI / 2)
    g.add(tw)
  }

  // square keep with corner turrets
  const KH = 7
  const KHALF = 3
  const keep = new THREE.Mesh(G.box, M.stoneKeep)
  keep.scale.set(KHALF * 2, KH, KHALF * 2)
  keep.position.y = KH / 2
  g.add(keep)
  crenelSquare(g, KHALF, KH, 5, [0.55, 0.6, 0.35])
  for (const c of [[1, 1], [1, -1], [-1, 1], [-1, -1]]) {
    const tur = new THREE.Mesh(new THREE.CylinderGeometry(0.62, 0.5, 1.8, 8), [M.stoneKeep, M.stoneTop, M.stoneKeep])
    tur.position.set(c[0] * KHALF, KH - 0.2, c[1] * KHALF)
    g.add(tur)
    const tg = new THREE.Group()
    tg.position.set(c[0] * KHALF, KH + 0.7, c[1] * KHALF)
    crenelRing(tg, 0.6, 0, 6, [0.24, 0.32, 0.18])
    g.add(tg)
  }
  for (let f = 0; f < 4; f++) {
    const fa = (f * Math.PI) / 2
    const nx = Math.sin(fa)
    const nz = Math.cos(fa)
    for (let row = 0; row < 2; row++) {
      for (let col = -1; col <= 1; col++) {
        const ox = Math.cos(fa) * col * 1.5
        const oz = -Math.sin(fa) * col * 1.5
        slit(g, nx * (KHALF + 0.03) + ox, 2.6 + row * 2.2, nz * (KHALF + 0.03) + oz, fa)
      }
    }
  }

  // the top tower, with a parapet and the beacon in a brazier
  const TH = 4.6
  const THALF = 1.4
  const topT = new THREE.Mesh(G.box, M.stoneKeep)
  topT.scale.set(THALF * 2, TH, THALF * 2)
  topT.position.y = KH + TH / 2
  g.add(topT)
  crenelSquare(g, THALF, KH + TH, 3, [0.45, 0.55, 0.3])
  for (let tf = 0; tf < 4; tf++) {
    const tfa = (tf * Math.PI) / 2
    slit(g, Math.sin(tfa) * (THALF + 0.03), KH + TH * 0.6, Math.cos(tfa) * (THALF + 0.03), tfa)
  }
  const top = KH + TH
  const brazier = new THREE.Mesh(new THREE.CylinderGeometry(0.55, 0.35, 0.5, 8), M.stoneDark)
  brazier.position.y = top + 0.25
  g.add(brazier)
  const beacon = new THREE.Mesh(new THREE.SphereGeometry(0.6, 14, 10), new THREE.MeshBasicMaterial({ color: 0xffd54a }))
  beacon.position.y = top + 0.85
  g.add(beacon)
  const halo = new THREE.Mesh(
    new THREE.SphereGeometry(0.6, 14, 10),
    new THREE.MeshBasicMaterial({ color: 0xffd54a, transparent: true, opacity: 0.4, depthWrite: false }),
  )
  halo.position.copy(beacon.position)
  g.add(halo)
  const bl = new THREE.PointLight(0xffd54a, 1.1, 30, 2)
  bl.position.copy(beacon.position)
  g.add(bl)

  const core = crystalAt(g, -1.6 - 12 - 1.8, 1.7)
  const ring = selectionRing(10, 0.1)
  ring.group.position.y = 0.1
  g.add(ring.group)
  return { group: g, core, ring, halo, light: bl, labelY: top + 3 }
}
