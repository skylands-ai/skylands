/**
 * The looks the 3D world can be drawn in. A theme changes only how the world
 * is drawn, never what it shows or the rest of the page. The choice is kept
 * in this browser.
 */

/** The themes drawn in code, which every citadel has. */
export type BuiltInThemeId = 'default' | 'scifi'

/** A theme's id: a built-in theme's, or a theme pack's that the world has. */
export type WorldThemeId = string

export interface WorldThemeInfo {
  id: BuiltInThemeId
  name: string
}

export const WORLD_THEMES: WorldThemeInfo[] = [
  { id: 'default', name: 'Default' },
  { id: 'scifi', name: 'Sci-fi' },
]

export function isBuiltInTheme(v: unknown): v is BuiltInThemeId {
  return WORLD_THEMES.some((t) => t.id === v)
}

/** Whether v could name a theme: a built-in one, or a pack, whose ids are lowercase words joined by hyphens. */
export function isWorldTheme(v: unknown): v is WorldThemeId {
  return typeof v === 'string' && v.length >= 2 && v.length <= 40 && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(v)
}
