import { describe, expect, it } from 'vitest'
import { cloneDir, isCloneURL } from './cloneurl'

describe('isCloneURL', () => {
  it('reads URLs and git’s user@host:path as clone URLs, as git does', () => {
    for (const u of ['https://github.com/org/shop.git', 'ssh://git@host/org/shop', 'git@github.com:org/shop.git', 'host:shop', ' https://x/y ', 'http://x/y', 'file:///etc']) {
      expect(isCloneURL(u), u).toBe(true)
    }
  })

  it('reads the rest as paths', () => {
    for (const u of ['shop', 'org/shop', '/home/me/code/shop', '~/code/shop', './a:b', 'a/b:c', '', ':shop']) {
      expect(isCloneURL(u), u).toBe(false)
    }
  })
})

describe('cloneDir', () => {
  it('names the directory git would clone into', () => {
    expect(cloneDir('https://github.com/org/shop.git')).toBe('shop')
    expect(cloneDir('https://github.com/org/shop/')).toBe('shop')
    expect(cloneDir('https://github.com/org/shop/.git')).toBe('shop')
    expect(cloneDir('git@github.com:org/shop.git')).toBe('shop')
    expect(cloneDir('github.com:shop')).toBe('shop')
    expect(cloneDir('ssh://host/r.bundle')).toBe('r')
  })

  it('names none when the URL gives none', () => {
    expect(cloneDir('https://x/..')).toBe('')
    expect(cloneDir('host:.')).toBe('')
  })
})
