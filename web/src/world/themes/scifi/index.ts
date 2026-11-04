import type { WorldTheme } from '../../theme'
import { buildAsteroid } from './asteroid'
import { starship } from './ships'
import { buildStation } from './station'
import { buildTerrace } from './terrace'

/**
 * A space station in place of the castle, asteroids with flattened landing
 * surfaces for islands, towers lit by landing-strip lights for terraces,
 * and starships for airships, among the stars.
 */
export const scifiTheme: WorldTheme = {
  id: 'scifi',
  buildCitadel: buildStation,
  buildIsland: buildAsteroid,
  buildTerrace,
  airship: starship,
  sky: {
    top: 0x02040b,
    horizon: 0x0e1533,
    fog: 0x060918,
    fogNear: 150,
    fogFar: 460,
    stars: 1,
    starsAround: true,
    clouds: false,
    hemiSky: 0xa9bcff,
    hemiGround: 0x2b2440,
    hemiIntensity: 0.75,
    sun: 0xffffff,
    sunIntensity: 1.15,
  },
}
