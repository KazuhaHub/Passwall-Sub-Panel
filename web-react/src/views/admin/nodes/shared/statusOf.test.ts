import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { statusOf, type NodeStatusFacts } from './statusOf'

// One case per row of the precedence table (design section 2.4): the first match wins, so each case also
// carries the conditions of every row below it, to prove the row above them really wins.
const line = (over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: 1, line_id: 'r1', name: 'L', enabled: true, position: 10, positioned: true, line_type: 'forward',
  published: true, withheld: '', health: { state: 'ok', checked_at: null }, ...over,
})
const direct = (over: Partial<OrderRow> = {}) => line({ line_id: 'direct', line_type: 'direct', ...over })
const facts = (over: Partial<NodeStatusFacts> = {}): NodeStatusFacts => ({ config_sync_state: 'synced', endpoint_in_sync: true, ...over })

describe('statusOf precedence', () => {
  it('1 entry missing wins even when the server calls the line published', () => {
    const s = statusOf(line({ entry: { panel_id: 9, panel_name: 'x', missing: true }, published: true, health: { state: 'unreachable', checked_at: null } }))
    expect(s).toMatchObject({ id: 'entry_missing', tone: 'danger', quiet: false })
  })
  it('2 landing disabled', () => expect(statusOf(line({ published: false, withheld: 'landing_disabled' }))).toMatchObject({ id: 'landing_disabled', tone: 'neutral', quiet: false }))
  it('3 hidden direct', () => expect(statusOf(direct({ published: false, withheld: 'hidden_direct' }))).toMatchObject({ id: 'hidden_direct', tone: 'neutral' }))
  it('4 line disabled', () => expect(statusOf(line({ published: false, withheld: 'disabled' }))).toMatchObject({ id: 'disabled', tone: 'neutral' }))
  it('5 waiting for the entry server is its own pending tone', () => expect(statusOf(line({ published: false, withheld: 'waiting_entry' }))).toMatchObject({ id: 'waiting_entry', tone: 'info' }))
  it('6 exit not ready', () => expect(statusOf(line({ published: false, withheld: 'egress' }))).toMatchObject({ id: 'egress', tone: 'warning' }))
  it('7 link down is danger', () => expect(statusOf(line({ published: false, withheld: 'probe' }))).toMatchObject({ id: 'probe', tone: 'danger' }))
  it('8 sources cannot be told apart', () => expect(statusOf(line({ published: false, withheld: 'source_guard' }))).toMatchObject({ id: 'source_guard', tone: 'warning' }))
  it('9 node cannot be served through CDN', () => expect(statusOf(line({ published: false, withheld: 'cdn_unfit' }))).toMatchObject({ id: 'cdn_unfit', tone: 'warning' }))
  it('10 a direct row shows an abandoned or pending config push', () => {
    expect(statusOf(direct(), facts({ config_sync_state: 'failed' }))).toMatchObject({ id: 'config_failed', tone: 'danger' })
    expect(statusOf(direct(), facts({ config_sync_state: 'pending' }))).toMatchObject({ id: 'config_pending', tone: 'warning' })
    // A relay row does not repeat node-level conditions.
    expect(statusOf(line(), facts({ config_sync_state: 'failed' }))).toMatchObject({ id: 'ok' })
  })
  it('11 endpoint drift needs an observation', () => {
    expect(statusOf(direct(), facts({ endpoint_in_sync: false, observed_protocol: 'trojan', observed_port: 8443 }))).toMatchObject({ id: 'endpoint_drift', tone: 'warning' })
    expect(statusOf(direct(), facts({ endpoint_in_sync: false }))).toMatchObject({ id: 'ok' })
  })
  it.each([['unreachable', 'unreachable'], ['panel_unreachable', 'server_unreachable'], ['inbound_missing', 'inbound_missing']])('12 health %s is danger', (state, id) =>
    expect(statusOf(line({ health: { state, checked_at: null } }))).toMatchObject({ id, tone: 'danger', quiet: false }))
  it.each([['inconclusive', 'inconclusive'], ['inbound_disabled', 'inbound_disabled']])('13 health %s is a warning', (state, id) =>
    expect(statusOf(line({ health: { state, checked_at: null } }))).toMatchObject({ id, tone: 'warning', quiet: false }))
  it('14 published and healthy is a quiet dot', () => expect(statusOf(line())).toMatchObject({ id: 'ok', tone: 'success', quiet: true }))
  it('15 published, never checked is a quiet neutral dot', () => expect(statusOf(line({ health: { state: 'unknown', checked_at: null } }))).toMatchObject({ id: 'unchecked', tone: 'neutral', quiet: true }))
  it('a withheld reason beats config and health conditions below it', () =>
    expect(statusOf(direct({ published: false, withheld: 'egress', health: { state: 'unreachable', checked_at: null } }), facts({ config_sync_state: 'failed' }))).toMatchObject({ id: 'egress' }))
})
