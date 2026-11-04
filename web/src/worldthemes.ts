/**
 * The looks the 3D world can be drawn in. A theme changes only how the world
 * is drawn, never what it shows or the rest of the page. The choice is kept
 * in this browser.
 */
export type WorldThemeId = 'default' | 'scifi'

export interface WorldThemeInfo {
  id: WorldThemeId
  name: string
  /** One line on what the world looks like in it. */
  blurb: string
}

export const WORLD_THEMES: WorldThemeInfo[] = [
  { id: 'default', name: 'Default', blurb: 'A castle and floating islands in a daytime sky.' },
  { id: 'scifi', name: 'Sci-fi', blurb: 'A space station and asteroids among the stars.' },
]

export function isWorldTheme(v: unknown): v is WorldThemeId {
  return WORLD_THEMES.some((t) => t.id === v)
}
