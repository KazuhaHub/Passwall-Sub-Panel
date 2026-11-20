import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { assignLandingColors } from '@/components/diagram/landingColor'
import { pathOf } from './pathOf'

const labels = { external: 'External entry', link: 'Server link', deleted: 'deleted' }
const colors = assignLandingColors([1])
const row = (over: Partial<OrderRow>): OrderRow => ({
  kind: 'line', node_id: 1, line_id: 'r', name: 'n', enabled: true, position: 1, positioned: true, published: true, withheld: '',
  landing: { node_id: 1, name: 'Taipei', region: '', panel_id: 5, panel_name: 'srv', enabled: true }, health: { state: 'ok', checked_at: null }, ...over,
})
const kinds = (r: OrderRow, o = {}) => pathOf(r, { colors, labels, ...o }).nodes.map(n => n.kind)

describe('pathOf', () => {
  it('draws each line type as the design maps it', () => {
    expect(kinds(row({ line_type: 'direct' }))).toEqual(['landing'])
    expect(kinds(row({ line_type: 'forward', entry: { panel_id: 2, panel_name: 'GZ', missing: false } }))).toEqual(['entry', 'landing'])
    expect(kinds(row({ line_type: 'cdn', address: 'cf.example' }))).toEqual(['cdn', 'landing'])
    expect(kinds(row({ line_type: 'external', address: 'x.example' }))).toEqual(['external', 'landing'])
  })
  it('a tunnel hop is dashed and a forward hop solid', () => {
    const entry = { panel_id: 2, panel_name: 'SH', missing: false }
    expect(pathOf(row({ line_type: 'tunnel', entry }), { colors, labels }).edges[0].style).toBe('dashed')
    expect(pathOf(row({ line_type: 'forward', entry }), { colors, labels }).edges[0].style).toBe('solid')
  })
  it('adds the exit when the landing leaves through another server, and the ends only on request', () => {
    expect(kinds(row({ line_type: 'direct' }), { exit: { name: 'OSA' } })).toEqual(['landing', 'exit'])
    expect(kinds(row({ line_type: 'direct' }), { exit: { name: 'OSA' }, ends: true })).toEqual(['user', 'landing', 'exit', 'internet'])
  })
  it('puts a withheld cause on the hop that causes it', () => {
    const entry = { panel_id: 2, panel_name: 'SH', missing: false }
    const waiting = pathOf(row({ line_type: 'tunnel', entry, published: false, withheld: 'waiting_entry' }), { colors, labels })
    expect(waiting.edges[0].status).toBe('pending')
    // Only the hop that causes it carries the tone, not the node at its end.
    expect(waiting.nodes.every(n => n.status === undefined)).toBe(true)
    const egress = pathOf(row({ line_type: 'direct', published: false, withheld: 'egress' }), { colors, labels, exit: { name: 'OSA' } })
    expect(egress.edges[0].status).toBe('warning')
  })
  it('names a deleted entry server on the entry node and dims a plainly disabled line as a whole', () => {
    const missing = pathOf(row({ line_type: 'forward', entry: { panel_id: 2, panel_name: 'SH', missing: true } }), { colors, labels })
    expect(missing.nodes[0]).toMatchObject({ status: 'danger', label: 'SH (deleted)' })
    const off = pathOf(row({ line_type: 'direct', published: false, withheld: 'disabled' }), { colors, labels })
    expect(off.nodes[0].status).toBe('neutral')
  })
})
