import { describe, expect, it } from 'vitest'
import { canonicalize } from './canonical'
import sample from '../../internal/canonical/testdata/rfc8785-sample.json?raw'
import sorting from '../../internal/canonical/testdata/rfc8785-sorting.json?raw'

// RFC 8785's own test vectors, from the same files the Go tests read.

const hex = (b: Uint8Array): string => [...b].map((x) => x.toString(16).padStart(2, '0')).join('')

describe('canonicalize', () => {
  it('writes the sample of RFC 8785 section 3.2.2 as the bytes of section 3.2.4', () => {
    const want = `7b 22 6c 69 74 65 72 61 6c 73 22 3a 5b 6e 75 6c 6c 2c 74 72
      75 65 2c 66 61 6c 73 65 5d 2c 22 6e 75 6d 62 65 72 73 22 3a
      5b 33 33 33 33 33 33 33 33 33 2e 33 33 33 33 33 33 33 2c 31
      65 2b 33 30 2c 34 2e 35 2c 30 2e 30 30 32 2c 31 65 2d 32 37
      5d 2c 22 73 74 72 69 6e 67 22 3a 22 e2 82 ac 24 5c 75 30 30
      30 66 5c 6e 41 27 42 5c 22 5c 5c 5c 5c 5c 22 2f 22 7d`.replace(/\s+/g, '')
    expect(hex(new TextEncoder().encode(canonicalize(JSON.parse(sample))))).toBe(want)
  })

  it('sorts the property names of RFC 8785 section 3.2.3 by UTF-16 code units', () => {
    const out = canonicalize(JSON.parse(sorting))
    const order = ['Carriage Return', 'One', 'Control', 'Latin Small Letter O With Diaeresis', 'Euro Sign', 'Emoji: Grinning Face', 'Hebrew Letter Dalet With Dagesh']
    const at = order.map((name) => out.indexOf(`"${name}"`))
    expect(at.every((i, n) => i >= 0 && (n === 0 || i > at[n - 1]))).toBe(true)
  })

  it('writes the numbers of RFC 8785 Appendix B', () => {
    const samples: [string, string][] = [
      ['0000000000000000', '0'],
      ['8000000000000000', '0'],
      ['0000000000000001', '5e-324'],
      ['8000000000000001', '-5e-324'],
      ['7fefffffffffffff', '1.7976931348623157e+308'],
      ['ffefffffffffffff', '-1.7976931348623157e+308'],
      ['4340000000000000', '9007199254740992'],
      ['c340000000000000', '-9007199254740992'],
      ['4430000000000000', '295147905179352830000'],
      ['44b52d02c7e14af5', '9.999999999999997e+22'],
      ['44b52d02c7e14af6', '1e+23'],
      ['44b52d02c7e14af7', '1.0000000000000001e+23'],
      ['444b1ae4d6e2ef4e', '999999999999999700000'],
      ['444b1ae4d6e2ef4f', '999999999999999900000'],
      ['444b1ae4d6e2ef50', '1e+21'],
      ['3eb0c6f7a0b5ed8c', '9.999999999999997e-7'],
      ['3eb0c6f7a0b5ed8d', '0.000001'],
      ['41b3de4355555553', '333333333.3333332'],
      ['41b3de4355555554', '333333333.33333325'],
      ['41b3de4355555555', '333333333.3333333'],
      ['41b3de4355555556', '333333333.3333334'],
      ['41b3de4355555557', '333333333.33333343'],
      ['becbf647612f3696', '-0.0000033333333333333333'],
      ['43143ff3c1cb0959', '1424953923781206.2'],
    ]
    const view = new DataView(new ArrayBuffer(8))
    for (const [bits, want] of samples) {
      view.setBigUint64(0, BigInt(`0x${bits}`))
      expect(canonicalize(view.getFloat64(0)), bits).toBe(want)
    }
    expect(() => canonicalize(NaN)).toThrow()
    expect(() => canonicalize(Infinity)).toThrow()
  })

  it('refuses lone surrogates', () => {
    expect(() => canonicalize(String.fromCharCode(0xd800) + 'x')).toThrow()
    expect(() => canonicalize({ [String.fromCharCode(0xdc00)]: 1 })).toThrow()
    expect(canonicalize(String.fromCharCode(0xd83d, 0xde00))).toBe('"😀"')
  })
})
