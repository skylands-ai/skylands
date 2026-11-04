import type { WorldThemeId } from '../../worldthemes'
import type { WorldTheme } from '../theme'
import { defaultTheme } from './default'
import { scifiTheme } from './scifi'

const THEMES: Record<WorldThemeId, WorldTheme> = { default: defaultTheme, scifi: scifiTheme }

/** The theme with an id, or the default one. */
export function themeFor(id: WorldThemeId): WorldTheme {
  return THEMES[id] ?? defaultTheme
}
