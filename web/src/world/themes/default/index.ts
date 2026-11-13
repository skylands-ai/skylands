import type { WorldTheme } from '../../theme'
import { airshipMesh } from './airship'
import { bird } from './bird'
import { buildCitadel } from './citadel'
import { buildIsland } from './island'
import { BUILDINGS, building, buildTerrace } from './terrace'

/** The original look: a stone castle, grassy floating islands, roped-off plazas, and airships, under a sky that follows the page's light or dark colors, where a bird flies past now and then. */
export const defaultTheme: WorldTheme = {
  id: 'default',
  buildCitadel,
  buildIsland,
  buildTerrace,
  building,
  buildings: BUILDINGS,
  airship: airshipMesh,
  sky: null,
  visitor: bird,
}
