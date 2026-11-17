import { describe, expect, it } from 'vitest'
import { approverTree, descendantCount, mayChange, type ApproverNode } from './approvers'
import type { ApproverInfo } from './protocol'
import type { State } from './store'

const WORLD = 'OWORLD'

function approver(key: string, by: string, over: Partial<ApproverInfo> = {}): ApproverInfo {
  return { key, name: key, enrolled: '2026-11-17T10:00:00Z', by, valid: true, ...over }
}

// laptop and desk were added with the owner key; the laptop added the
// phone and the watch, and the phone added the tablet.
const approvers = [
  approver('laptop', 'owner', { through: ['phone', 'tablet', 'watch'] }),
  approver('desk', 'owner'),
  approver('phone', 'laptop', { through: ['tablet'] }),
  approver('tablet', 'phone'),
  approver('watch', 'laptop'),
]

const as = (key: string, list = approvers): State => ({ world: { owner: WORLD }, me: { key, world: WORLD }, approvers: list }) as unknown as State

describe('mayChange', () => {
  it('lets a browser the owner key added change any approver', () => {
    expect(['laptop', 'desk', 'phone', 'tablet', 'watch'].every((k) => mayChange(as('laptop'), k))).toBe(true)
    expect(mayChange(as('desk'), 'tablet')).toBe(true)
  })

  it('lets a browser added by a link change only itself and those added through it', () => {
    expect(mayChange(as('phone'), 'phone')).toBe(true)
    expect(mayChange(as('phone'), 'tablet')).toBe(true)
    expect(mayChange(as('phone'), 'laptop')).toBe(false)
    expect(mayChange(as('phone'), 'watch')).toBe(false)
    expect(mayChange(as('phone'), 'desk')).toBe(false)
    expect(mayChange(as('tablet'), 'phone')).toBe(false)
  })

  it('lets a browser that no longer approves change nothing', () => {
    const revoked = approvers.map((a) => (a.key === 'desk' ? { ...a, valid: false } : a))
    expect(mayChange(as('desk', revoked), 'desk')).toBe(false)
    expect(mayChange(as('stranger'), 'phone')).toBe(false)
  })
})

describe('approverTree', () => {
  const shape = (nodes: ApproverNode[]): unknown[] => nodes.map((n) => (n.children.length ? { [n.approver.key]: shape(n.children) } : n.approver.key))

  it('puts the approvers the owner key added at the top, with those their links added under them', () => {
    expect(shape(approverTree(approvers))).toEqual([{ laptop: [{ phone: ['tablet'] }, 'watch'] }, 'desk'])
  })

  it('leaves out the approvers that no longer approve', () => {
    const list = approvers.map((a) => (a.key === 'phone' || a.key === 'tablet' ? { ...a, valid: false } : a))
    expect(shape(approverTree(list))).toEqual([{ laptop: ['watch'] }, 'desk'])
  })

  it('draws an approver whose parent it doesn’t list as a root', () => {
    expect(shape(approverTree([approver('phone', 'gone')]))).toEqual(['phone'])
  })

  it('counts the approvers under one at any depth', () => {
    const [laptop, desk] = approverTree(approvers)
    expect(descendantCount(laptop)).toBe(3)
    expect(descendantCount(desk)).toBe(0)
  })
})
