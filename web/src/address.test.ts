import { describe, expect, it } from 'vitest'
import { citadelAddress, linkAt } from './address'

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
