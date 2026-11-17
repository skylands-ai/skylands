import { describe, expect, it } from 'vitest'
import { citadelAddress, linkAt, loopbackMove } from './address'

describe('citadelAddress', () => {
  it('takes an http or https address with a host, as --url does', () => {
    expect(citadelAddress('https://box.example.ts.net')).toEqual({ address: 'https://box.example.ts.net' })
    expect(citadelAddress(' https://box.example.ts.net/ ')).toEqual({ address: 'https://box.example.ts.net' })
    expect(citadelAddress('http://192.168.1.20:8080')).toEqual({ address: 'http://192.168.1.20:8080' })
  })

  it('refuses anything else, saying why', () => {
    for (const raw of ['box.example.ts.net', 'box:8080', 'ftp://box', 'https://box/skylands', 'https://box/?x=1', 'https://box/#setup', 'https://someone@box', '']) {
      expect(citadelAddress(raw)).toHaveProperty('error')
    }
  })
})

describe('linkAt', () => {
  it('keeps the claim and changes only the address', () => {
    expect(linkAt('http://127.0.0.1:8080/setup#approver=SUABC', 'https://box.example.ts.net')).toBe('https://box.example.ts.net/setup#approver=SUABC')
  })
})

describe('loopbackMove', () => {
  const home = 'http://127.0.0.1:8080'
  const at = (url: string) => new URL(url)

  it('moves a page at another loopback name on the same port to the citadel, keeping its path and code', () => {
    expect(loopbackMove(home, at('http://localhost:8080/inbox?x=1#y'))).toBe('http://127.0.0.1:8080/inbox?x=1#y')
    expect(loopbackMove(home, at('http://[::1]:8080/setup#approver=SEED'))).toBe('http://127.0.0.1:8080/setup#approver=SEED')
  })

  it('leaves a page where it is otherwise', () => {
    expect(loopbackMove(home, at('http://127.0.0.1:8080/'))).toBeNull()
    // Behind a proxy that sends the citadel Host: localhost:8080.
    expect(loopbackMove(home, at('https://box.example.ts.net/inbox'))).toBeNull()
    // A tunnel to another local port.
    expect(loopbackMove(home, at('http://localhost:8081/'))).toBeNull()
    expect(loopbackMove('https://box.example.ts.net', at('http://localhost:8080/'))).toBeNull()
    expect(loopbackMove(undefined, at('http://localhost:8080/'))).toBeNull()
    expect(loopbackMove('not a url', at('http://localhost:8080/'))).toBeNull()
  })
})
