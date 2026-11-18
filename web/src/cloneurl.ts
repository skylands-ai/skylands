/**
 * Whether what is typed is a clone URL rather than a path, as git reads
 * it: a URL with a scheme, or git's user@host:path, whose part before the
 * first colon has no slash. The island refuses all but https and ssh.
 */
export function isCloneURL(text: string): boolean {
  const t = text.trim()
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(t)) return true
  const colon = t.indexOf(':')
  return colon > 0 && !t.slice(0, colon).includes('/')
}

/** The directory git clones url into when given none: the URL's last part, without .git. */
export function cloneDir(url: string): string {
  const u = url.trim().replace(/\/+$/, '').replace(/\/\.git$/, '').replace(/\/+$/, '')
  const name = u
    .slice(Math.max(u.lastIndexOf('/'), u.lastIndexOf(':')) + 1)
    .replace(/\.git$/, '')
    .replace(/\.bundle$/, '')
  return name === '.' || name === '..' ? '' : name
}
