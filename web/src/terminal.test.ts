import { describe, expect, it } from 'vitest'
import type { Session } from './protocol'
import type { State } from './store'
import { canTerminal, convoMode, fromBase64, INPUT_PIECE, newTerminalId, OutputFollower, pieces, toBase64 } from './terminal'

const enc = new TextEncoder()
const dec = new TextDecoder()
const out = (off: number, text: string) => ({ off, data: toBase64(enc.encode(text)) })
const text = (parts: Uint8Array[]) => parts.map((p) => dec.decode(p)).join('')

describe('OutputFollower', () => {
  it('draws the screen, then what arrived while it was read, past it', () => {
    const f = new OutputFollower()
    // Held while the screen is read: one piece the screen has, one it overlaps, one after.
    expect(f.push(out(0, 'abc'))).toEqual({ gap: false })
    f.push(out(3, 'def'))
    f.push(out(6, 'gh'))
    const r = f.reset(out(2, 'cde'))
    expect(r.gap).toBe(false)
    expect(text(r.draw)).toBe('cdefgh')
    expect(f.next).toBe(8)
  })

  it('draws live pieces in order, skips what it has, and notices a gap', () => {
    const f = new OutputFollower()
    f.reset(out(10, 'hello'))
    expect(text([f.push(out(15, ' there')).draw!])).toBe(' there')
    expect(f.push(out(10, 'hello')).draw).toBeUndefined()
    expect(text([f.push(out(19, 're, you')).draw!])).toBe(', you')
    expect(f.push(out(40, 'later')).gap).toBe(true)
  })

  it('holds everything again once it is told to', () => {
    const f = new OutputFollower()
    f.reset(out(0, 'x'))
    f.hold()
    expect(f.next).toBe(-1)
    expect(f.push(out(5, 'y'))).toEqual({ gap: false })
    // The screen read afterwards starts past the held piece, which drops.
    expect(text(f.reset(out(3, 'abcd')).draw)).toBe('abcd')
  })

  it('takes a screen with nothing on it yet, as Go sends it, as no bytes', () => {
    const f = new OutputFollower()
    f.push(out(0, '\x1b]666;x\x1b\\$ '))
    const r = f.reset({ off: 0, data: null as unknown as string })
    expect(f.next).toBe(11)
    expect(text(r.draw)).toBe('\x1b]666;x\x1b\\$ ')
  })

  it('reports a gap among the held pieces', () => {
    const f = new OutputFollower()
    f.push(out(20, 'far'))
    expect(f.reset(out(0, 'near')).gap).toBe(true)
  })
})

describe('input', () => {
  it('goes in pieces no longer than a message carries', () => {
    const big = new Uint8Array(INPUT_PIECE * 2 + 5).fill(65)
    const ps = pieces(big)
    expect(ps.map((p) => p.length)).toEqual([INPUT_PIECE, INPUT_PIECE, 5])
    expect(pieces(enc.encode('ls\r')).length).toBe(1)
  })

  it('round-trips bytes through base64, as Go writes []byte', () => {
    const bytes = Uint8Array.from({ length: 300000 }, (_, i) => (i * 7) % 256)
    expect(fromBase64(toBase64(bytes))).toEqual(bytes)
    expect(toBase64(enc.encode('é\r\x03'))).toBe('w6kNAw==')
  })

  it('names terminals with letters and digits, as the citadel takes them', () => {
    expect(newTerminalId()).toMatch(/^[A-Za-z0-9]{22}$/)
    expect(newTerminalId()).not.toBe(newTerminalId())
  })
})

describe('convoMode', () => {
  const x = (status: string, terminals = 0) => ({ id: 'S1', status, terminals: Array.from({ length: terminals }, (_, i) => ({ id: `T${i}`, shell: '/bin/sh', openedAt: '' })) }) as unknown as Session
  const s = (mode?: 'chat' | 'terminal') => ({ convoMode: mode ? { S1: mode } : {} }) as unknown as State

  it('shows the chat unless switched to the terminal', () => {
    expect(convoMode(s(), x('idle'))).toBe('chat')
    expect(convoMode(s('terminal'), x('idle'))).toBe('terminal')
    expect(convoMode(s('terminal'), x('running'))).toBe('terminal')
  })

  it('goes back to the chat once the session is over and its terminals closed', () => {
    expect(canTerminal(x('ended'))).toBe(false)
    expect(convoMode(s('terminal'), x('ended'))).toBe('chat')
    // A failed session keeps the terminals open in it until they close.
    expect(convoMode(s('terminal'), x('failed', 1))).toBe('terminal')
  })
})
