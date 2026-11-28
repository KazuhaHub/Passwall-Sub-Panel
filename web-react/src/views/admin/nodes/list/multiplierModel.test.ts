import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { groupByServer, multiplierLinesOf } from './multiplierModel'
import type { ListRow } from './types'

const item = (node: number, line: string, server: number, milli?: number): ListRow => ({
  id: `${node}.${line}`,
  row: { kind: 'line', node_id: node, line_id: line, name: `line ${line}`, enabled: true, position: 0, positioned: true, traffic_factor_milli: milli, landing: { panel_id: server, name: `N${node}` } as never } as OrderRow,
})
const items = [item(1, 'direct', 10, 2000), item(1, 'r1', 10), item(2, 'direct', 11, 500), item(3, 'direct', 10)]

describe('multiplier lines of a selection', () => {
  it('expands a landing row to all of its lines and lists each line once', () => {
    const lines = multiplierLinesOf(items, ['landing:1', '1.r1', '2.direct'])
    expect(lines.map(l => l.key)).toEqual(['1.direct', '1.r1', '2.direct'])
    expect(lines.map(l => l.old)).toEqual([2000, 1000, 500])
    expect(lines[1].label).toBe('N1 / line r1')
  })
  it('groups changes per server', () => {
    const groups = groupByServer(multiplierLinesOf(items, ['1.direct', '2.direct', '3.direct']), 1500)
    expect(groups).toEqual([
      { serverId: 10, changes: [{ node_id: 1, line_id: 'direct', traffic_factor_milli: 1500 }, { node_id: 3, line_id: 'direct', traffic_factor_milli: 1500 }] },
      { serverId: 11, changes: [{ node_id: 2, line_id: 'direct', traffic_factor_milli: 1500 }] },
    ])
  })
})
