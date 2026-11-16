import { describe, expect, it } from 'vitest'
import { cliCommand } from './address'

describe('cliCommand', () => {
  it('quotes the invite link for a shell', () => {
    expect(cliCommand('https://skylands.example.com/setup#approver=SUABC')).toBe("skylands approver add 'https://skylands.example.com/setup#approver=SUABC'")
  })
  it('keeps a quote in the link from ending the argument', () => {
    expect(cliCommand("http://127.0.0.1:8080/setup#approver=it's")).toBe("skylands approver add 'http://127.0.0.1:8080/setup#approver=it'\\''s'")
  })
})
