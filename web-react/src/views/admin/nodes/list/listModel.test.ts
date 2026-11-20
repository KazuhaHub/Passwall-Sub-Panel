import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import { buildTree, facetOptions, filterOrder, kpisOf, landingColorsOf, NO_FILTERS, relatedIds, searchText } from './listModel'
import { rowId } from './orderMoves'

const L = (node: number, id: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name: `${node}.${id}`, enabled: true, position: 0, positioned: true, published: true, withheld: '',
  line_type: id === 'direct' ? 'direct' : 'forward', health: { state: 'ok', checked_at: null },
  landing: { node_id: node, name: `Node${node}`, region: 'TW', panel_id: node * 10, panel_name: `srv${node}`, enabled: true },
  ...(id === 'direct' ? {} : { entry: { panel_id: 7, panel_name: 'GZ', missing: false } }), ...over,
})
const S = (id: number, name: string): OrderRow => ({ kind: 'separator', id, name, enabled: true, position: 0, positioned: true })
const rows = [S(1, '---- HK ----'), L(1, 'direct'), L(1, 'a'), L(2, 'direct', { published: false, withheld: 'disabled' }), L(2, 'b', { health: { state: 'unreachable', checked_at: null } })]
const nodes = new Map<number, Node>([[1, { tags: ['premium'], region: 'TW' } as Node]])
const f = (over = {}) => ({ ...NO_FILTERS, ...over })
const ids = (r: OrderRow[]) => r.map(rowId)

describe('list filtering', () => {
  it('search matches names, servers, address, tags and the node id, all words', () => {
    expect(ids(filterOrder(rows, nodes, f({ q: 'gz node1' })))).toEqual(['1.a'])
    expect(ids(filterOrder(rows, nodes, f({ q: 'premium' })))).toEqual(['1.direct', '1.a'])
    expect(ids(filterOrder(rows, nodes, f({ q: '#2' })))).toEqual(['2.direct', '2.b'])
    expect(searchText(rows[1], nodes.get(1))).toContain('#1')
  })
  it('without a filter every row is shown, separators included', () => {
    expect(ids(filterOrder(rows, nodes, f()))).toEqual(ids(rows))
  })
  it('a separator is shown while filtering only when the keyword matches its name and no facet is set', () => {
    expect(ids(filterOrder(rows, nodes, f({ q: 'hk' })))).toEqual(['sep:1'])
    expect(ids(filterOrder(rows, nodes, f({ q: 'hk', type: ['forward'] })))).toEqual([])
    expect(ids(filterOrder(rows, nodes, f({ type: ['forward'] })))).toEqual(['1.a', '2.b'])
  })
  it('status facet: published, not published, unreachable and each withheld reason', () => {
    expect(ids(filterOrder(rows, nodes, f({ status: ['unpublished'] })))).toEqual(['2.direct'])
    expect(ids(filterOrder(rows, nodes, f({ status: ['unreachable'] })))).toEqual(['2.b'])
    expect(ids(filterOrder(rows, nodes, f({ status: ['disabled'] })))).toEqual(['2.direct'])
    expect(ids(filterOrder(rows, nodes, f({ entry: ['7'], landing: ['10'] })))).toEqual(['1.a'])
  })
  it('KPIs count lines, not separators', () => {
    expect(kpisOf(rows)).toEqual({ lines: 4, published: 3, unpublished: 1, unreachable: 1 })
    expect(facetOptions(rows).entry).toEqual([{ value: '7', label: 'GZ', count: 2 }])
  })
})

describe('landing colours, highlight and tree', () => {
  it('colours come from the complete list sorted by id, so a filter or a drag cannot recolour', () => {
    const c = landingColorsOf(rows)
    expect(c.get('1')?.index).toBe(1)
    expect(landingColorsOf([...rows].reverse()).get('2')).toEqual(c.get('2'))
  })
  it('hover lights the same landing and the same entry server', () => {
    expect(relatedIds(rows, '1.a').sort()).toEqual(['1.direct', '2.b'].sort())
    expect(relatedIds(rows, 'sep:1')).toEqual([])
  })
  it('the by-node tree puts landings in the order of their direct row and leaves separators out', () => {
    const swapped = [rows[3], rows[1], rows[0], rows[2], rows[4]]
    const tree = buildTree(swapped, swapped)
    expect(tree.map(t => [t.node_id, t.children.map(rowId)])).toEqual([[2, ['2.b']], [1, ['1.a']]])
    // A filter that hid the direct row keeps the group.
    const kept = buildTree([rows[2]], rows)[0]
    expect(kept.node_id).toBe(1)
    expect(kept.direct).toBeUndefined()
  })
})
