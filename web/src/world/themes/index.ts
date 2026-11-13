import { isBuiltInTheme, type BuiltInThemeId } from '../../worldthemes'
import type { WorldTheme } from '../theme'
import { defaultTheme } from './default'
import { scifiTheme } from './scifi'

const THEMES: Record<BuiltInThemeId, WorldTheme> = { default: defaultTheme, scifi: scifiTheme }

export { defaultTheme }

/** The built-in theme with an id, or undefined for a pack's. */
export function builtInTheme(id: string): WorldTheme | undefined {
  return isBuiltInTheme(id) ? THEMES[id] : undefined
}
