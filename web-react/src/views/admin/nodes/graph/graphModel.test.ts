import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { buildGraph, edgeCountOf, graphTooLarge, MAX_GRAPH_EDGES, type GraphLabels } from './graphModel'

const labels: GraphLabels = {
  direct: 'Direct', cdn: 'CDN', external: 'External', local: 'Own server', link: 'Server link', deleted: n => `${n} (deleted)`,
  entry: 'Entry', landing: 'Landing', exit: 'Exit',
}
const landing = (id: number) => ({ node_id: id, name: `Node${id}`, region: '', panel_id: id * 10, panel_name: `srv${id}`, enabled: true })
const row = (node: number, id: string, type: OrderRow['line_type'], over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name: `${node}.${id}`, enabled: true, position: 1, positioned: true, line_type: type,
  published: true, withheld: '', address: 'x.example', landing: landing(node), health: { state: 'ok', checked_at: null }, ...over,
})
const via = (id: number, name: string, missing = false) => ({ entry: { panel_id: id, panel_name: name, missing } })
const rows = [
  row(1, 'direct', 'direct'),
  row(1, 'a', 'forward', via(7, 'GZ')),
  row(1, 'b', 'tunnel', { ...via(8, 'SH'), published: false, withheld: 'waiting_entry' }),
  row(2, 'c', 'cdn', { address: 'cf.example' }),
  row(2, 'd', 'external'),
  row(2, 'e', 'forward', via(9, 'GONE', true)),
]
const noExit = () => null

describe('buildGraph', () => {
  it('puts one box per entry server and one each for direct, CDN and external, so the column is bounded by the servers', () => {
    const g = buildGraph(rows, { colors: new Map(), exitOf: noExit, labels })
    const entries = g.columns[0]
    expect(entries.title).toBe('Entry')
    expect(entries.boxes.map(b => b.label)).toEqual(['Direct', 'GZ', 'SH', 'CDN', 'External', 'GONE (deleted)'])
    expect(g.columns[1].boxes.map(b => b.label)).toEqual(['Node1', 'Node2'])
    // No landing leaves through another server, so there is no exit column at all.
    expect(g.columns).toHaveLength(2)
  })

  it('draws one edge per line, dashed for a tunnel, with the cause of a problem as the edge status', () => {
    const g = buildGraph(rows, { colors: new Map(), exitOf: noExit, labels })
    const byId = Object.fromEntries(g.edges.map(e => [e.id, e]))
    expect(Object.keys(byId)).toHaveLength(6)
    expect(byId['1.b']).toMatchObject({ from: 'entry:8', to: 'landing:1', style: 'dashed', status: 'pending', label: '1.b' })
    expect(byId['1.a']).toMatchObject({ from: 'entry:7', to: 'landing:1', style: 'solid', status: undefined })
    expect(byId['2.c'].from).toBe('cdn')
    expect(byId['1.direct'].from).toBe('direct')
  })

  it('draws a line whose entry server was deleted, flagged, instead of leaving it out', () => {
    const g = buildGraph(rows, { colors: new Map(), exitOf: noExit, labels })
    expect(g.edges.find(e => e.id === '2.e')).toMatchObject({ from: 'entry:9', status: 'danger' })
    expect(g.columns[0].boxes.find(b => b.id === 'entry:9')?.status).toBe('danger')
  })

  it('adds the exit column only when a landing leaves through another server, joining every landing to it', () => {
    const g = buildGraph(rows, { colors: new Map(), exitOf: id => (id === 2 ? { id: 40, name: 'OSA' } : null), labels })
    expect(g.columns).toHaveLength(3)
    expect(g.columns[2].boxes.map(b => b.label)).toEqual(['Own server', 'OSA'])
    const exits = g.edges.filter(e => e.id.startsWith('exit:'))
    expect(exits.map(e => [e.from, e.to, e.style])).toEqual([['landing:1', 'exit:local', 'dashed'], ['landing:2', 'exit:40', 'dashed']])
  })

  it('says, for a box, which list filter shows its lines (entry server, type) and where a landing node opens', () => {
    const g = buildGraph(rows, { colors: new Map(), exitOf: noExit, labels })
    expect(g.actions['entry:7']).toEqual({ kind: 'facet', facet: 'entry', value: '7', lines: 1 })
    expect(g.actions.cdn).toEqual({ kind: 'facet', facet: 'type', value: 'cdn', lines: 1 })
    expect(g.actions['landing:2']).toEqual({ kind: 'node', nodeId: 2, lines: 3 })
  })
})

describe('the size rule', () => {
  it('counts the lines plus one exit edge per landing when there is an exit column', () => {
    expect(edgeCountOf(rows, noExit)).toBe(6)
    expect(edgeCountOf(rows, id => (id === 2 ? { id: 40, name: 'OSA' } : null))).toBe(8)
  })
  it('refuses to draw more than the cap and says so before building anything', () => {
    expect(graphTooLarge(MAX_GRAPH_EDGES)).toBe(false)
    expect(graphTooLarge(MAX_GRAPH_EDGES + 1)).toBe(true)
  })
})
