import type { WorldTheme } from '../../theme'
import { airshipMesh } from './airship'
import { buildCitadel } from './citadel'
import { buildIsland } from './island'
import { buildTerrace } from './terrace'

/** The original look: a stone castle, grassy floating islands, roped-off plazas, and airships, under a sky that follows the page's light or dark colors. */
export const defaultTheme: WorldTheme = {
  id: 'default',
  buildCitadel,
  buildIsland,
  buildTerrace,
  airship: airshipMesh,
  sky: null,
}
