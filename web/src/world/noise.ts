/** A stable hash in [0, 1) of a point, so geometry jitter is the same on every visit. */
export function hash3(x: number, y: number, z: number): number {
  let h = Math.imul(Math.round(x * 97) | 0, 73856093) ^ Math.imul(Math.round(y * 97) | 0, 19349663) ^ Math.imul(Math.round(z * 97) | 0, 83492791)
  h = Math.imul(h ^ (h >>> 13), 1274126177)
  return ((h ^ (h >>> 16)) >>> 0) / 4294967295
}

/** A seeded random sequence in [0, 1). */
export function rng(seed: number): () => number {
  let s = seed >>> 0
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0
    return s / 4294967296
  }
}

/** A smooth radial wobble around a ring, seeded so each island keeps its outline. */
export function ringNoise(seed: number): (t: number) => number {
  const r = rng(seed)
  const a = r() * 6.283
  const b = r() * 6.283
  const c = r() * 6.283
  return (t) => 1 + 0.09 * Math.sin(3 * t + a) + 0.06 * Math.sin(5 * t + b) + 0.035 * Math.sin(9 * t + c)
}
