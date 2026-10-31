import type { Repo, TerrainFile } from './protocol'

/** The files a terrace shows, largest first. A building's index is its position here. */
export function sortedFiles(repo: Repo): TerrainFile[] {
  return [...repo.files].sort((a, b) => b.size - a.size || a.path.localeCompare(b.path))
}

export function base(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? path : path.slice(i + 1)
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export function shortCommit(c?: string): string {
  return c ? c.slice(0, 7) : 'none'
}
