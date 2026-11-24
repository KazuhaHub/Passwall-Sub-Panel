import { describe, expect, it } from 'vitest'
import type { ProxyGroupMember } from '@/api/rules'
import { continuation } from './continuation'

const g = (value: string): ProxyGroupMember => ({ kind: 'proxy_group', value })
const b = (value: string): ProxyGroupMember => ({ kind: 'builtin', value })
const node: ProxyGroupMember = { kind: 'node', node_id: 1 }

// Mihomo walks into a group's current selection and keeps matching when it resolves to PASS (tunnel/tunnel.go match):
// a rule that names such a group is not the last word, and nor is a Rematch outbound.
describe('a target that may not be final', () => {
  const groups = new Map<string, ProxyGroupMember[]>([
    ['Plain', [b('DIRECT'), node, { kind: 'node_set', value: 'remaining' }]],
    ['Udp', [b('DIRECT'), b('PASS'), g('Plain'), b('REJECT')]],
    ['ViaUdp', [g('Udp'), node]],
    ['ViaPlain', [g('Plain')]],
    ['Loop1', [g('Loop2')]],
    ['Loop2', [g('Loop1'), node]],
    ['LoopPass', [g('LoopPass2')]],
    ['LoopPass2', [g('LoopPass'), b('PASS')]],
    ['Rem', [{ kind: 'rematch', value: 'R' }]],
  ])
  const c = continuation(groups, ['R'])

  it('built-in outbounds: DIRECT, REJECT and REJECT-DROP are final, PASS is not', () => {
    for (const t of ['DIRECT', 'REJECT', 'REJECT-DROP']) expect(c.mayContinue(t), t).toBe(false)
    expect(c.mayContinue('PASS')).toBe(true)
  })

  it('a group that offers PASS as a member may continue, and so does one that reaches such a group through its members', () => {
    expect(c.mayContinue('Udp')).toBe(true)
    expect(c.mayContinue('ViaUdp')).toBe(true)
  })

  it('a group whose members cannot reach PASS is final', () => {
    expect(c.mayContinue('Plain')).toBe(false)
    expect(c.mayContinue('ViaPlain')).toBe(false)
  })

  it('cycles are walked once', () => {
    expect(c.mayContinue('Loop1')).toBe(false)
    expect(c.mayContinue('LoopPass')).toBe(true)
  })

  it('a Rematch outbound is never final, as a target and as a member', () => {
    expect(c.mayContinue('R')).toBe(true)
    expect(c.mayContinue('Rem')).toBe(true)
  })

  it('what is not known is taken as may-continue: a group missing from the data, or no data at all', () => {
    expect(c.mayContinue('Nobody')).toBe(true)
    expect(continuation(null, []).mayContinue('Plain')).toBe(true)
    expect(continuation(null, []).mayContinue('DIRECT')).toBe(false)
  })

  it('a member group that the data does not list is unknown, so the group may continue', () => {
    expect(continuation(new Map([['A', [g('Missing')]]]), []).mayContinue('A')).toBe(true)
  })
})
