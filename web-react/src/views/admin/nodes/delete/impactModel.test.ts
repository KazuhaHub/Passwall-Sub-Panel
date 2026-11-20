import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import type { FrontError } from '@/api/fronts'
import type { TunnelPlanView } from '@/utils/tunnelConfirm'
import { entryLookup, impactViewOf, planCovered, serverImpactModel, totalLines } from './impactModel'

const plan = (params: Record<string, unknown>, action = 'delete_node'): TunnelPlanView => ({
  action, title: '', message: '', lines: [], error: { status: 409, code: 'confirm_required', params: { kind: 'tunnel', action, ...params }, message: '' } as FrontError,
})
const tunnel = { node_id: 9, line_id: 'L1', line_name: 'GZ', entry_panel_id: 2, port: 40123 }

describe('impactViewOf', () => {
  const data = { node: { id: 1, name: 'A' }, lines: [{ node_id: 1, line_id: 'direct', landing_name: 'A', line_name: 'A', line_type: 'direct' as const }], tunnels: [], blocked_by_routes: [] }
  it('is loading until the read answers, failed when it fails, never ready without data', () => {
    expect(impactViewOf({ isPending: true, isError: false, data: undefined, error: null }, 'delete').kind).toBe('loading')
    expect(impactViewOf({ isPending: false, isError: true, data: undefined, error: new Error('x') }, 'delete').kind).toBe('failed')
    expect(impactViewOf({ isPending: false, isError: false, data, error: null }, 'delete').kind).toBe('ready')
  })
  it('blocks a delete and a remove-from-panel alike behind shared-port routes (the server checks them for both)', () => {
    const blocked = { ...data, blocked_by_routes: ['shared port 443, route a.com'] }
    const q = { isPending: false, isError: false, data: blocked, error: null }
    expect(impactViewOf(q, 'delete')).toMatchObject({ kind: 'blocked', routes: ['shared port 443, route a.com'] })
    expect(impactViewOf(q, 'detach').kind).toBe('blocked')
  })
})

describe('planCovered', () => {
  it('accepts a plan that only removes the listeners the dialog already listed', () => {
    expect(planCovered(plan({ removes: [{ node_id: 9, line_id: 'L1', entry_panel_id: 2, port: 40123 }] }), [tunnel])).toBe(true)
  })
  it('does not accept a plan with something the dialog did not show', () => {
    expect(planCovered(plan({ removes: [{ line_id: 'L2', entry_panel_id: 2, port: 40999 }] }), [tunnel])).toBe(false)
    expect(planCovered(plan({ removes: [], creates: [{ line_id: 'x' }] }), [tunnel])).toBe(false)
    expect(planCovered(plan({ removes: [{ line_id: 'L1', entry_panel_id: 2, port: 40123 }] }, 'remove_line'), [tunnel])).toBe(false)
  })
})

describe('entryLookup and totals', () => {
  const rows = [
    { kind: 'line', node_id: 1, line_id: 'a', name: 'a', line_type: 'forward', entry: { panel_id: 7, panel_name: 'GZ', missing: false }, address: 'gz.example' },
    { kind: 'line', node_id: 1, line_id: 'c', name: 'c', line_type: 'cdn', address: 'cf.example' },
  ] as OrderRow[]
  it('names the entry server from the order rows, or the address of a CDN or external entry', () => {
    const entryOf = entryLookup(rows)
    expect(entryOf({ node_id: 1, line_id: 'a' } as never)).toBe('GZ')
    expect(entryOf({ node_id: 1, line_id: 'c' } as never)).toBe('cf.example')
    expect(entryOf({ node_id: 1, line_id: 'direct' } as never)).toBeUndefined()
  })
  it('counts lines over the nodes that can be deleted only', () => {
    expect(totalLines([{ lines: [1, 2, 3] }, { lines: [1] }] as never)).toBe(4)
  })
})

describe('serverImpactModel', () => {
  const base = { server_id: 3, nodes: [], lines_entering: [], egress_users: [], route_users: [] }
  it('does not block on the nodes that only use the server as their exit (the server does not refuse that)', () => {
    const m = serverImpactModel({ ...base, egress_users: [{ node_id: 1, node: 'A' }] })
    expect(m.blocking).toBe(0)
    expect(m.egress).toHaveLength(1)
  })
  it('counts nodes, entering lines and route users as the dependencies that block', () => {
    const m = serverImpactModel({
      ...base, nodes: [{ node_id: 1, node: 'A' }], route_users: [{ node_id: 2, node: 'B', routes: ['r1'] }],
      lines_entering: [{ node_id: 4, line_id: 'x', landing_name: 'D', line_name: 'L', line_type: 'forward', landing_pending_delete: true }],
    })
    expect(m.blocking).toBe(3)
    expect(m.pendingLandings).toBe(true)
  })
})
