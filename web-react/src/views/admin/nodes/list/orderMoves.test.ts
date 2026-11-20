import { describe, expect, it } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { applyIntent, diffOrders, intentOfDrag, isNoop, itemsOf, otherLinesOf, rowId } from './orderMoves'

const line = (node: number, id = 'direct'): OrderRow => ({ kind: 'line', node_id: node, line_id: id, name: `${node}.${id}`, enabled: true, position: 0, positioned: true })
const sep = (id: number): OrderRow => ({ kind: 'separator', id, name: `sep${id}`, enabled: true, position: 0, positioned: true })
const ids = (rows: OrderRow[] | null) => rows?.map(rowId)
const base = [line(1), line(1, 'a'), sep(1), line(2), line(2, 'b')]

describe('order moves', () => {
  it('the PUT items are the whole list, typed, top to bottom', () => {
    expect(itemsOf(base)).toEqual([
      { kind: 'line', node_id: 1, line_id: 'direct' }, { kind: 'line', node_id: 1, line_id: 'a' }, { kind: 'separator', id: 1 },
      { kind: 'line', node_id: 2, line_id: 'direct' }, { kind: 'line', node_id: 2, line_id: 'b' },
    ])
  })
  it('a separator and a line may share an id number without colliding', () => {
    expect(rowId(sep(1))).not.toBe(rowId(line(1)))
  })
  it('turns a drag report into "put after the row now above it" and applies it', () => {
    const intent = intentOfDrag(base, { movedId: '2.b', beforeId: '1.a' })!
    expect(intent).toEqual({ moved: ['2.b'], after: '1.direct' })
    expect(ids(applyIntent(base, intent))).toEqual(['1.direct', '2.b', '1.a', 'sep:1', '2.direct'])
    expect(intentOfDrag(base, { movedId: '2.b', beforeId: '1.direct' })!.after).toBeNull()
  })
  it('moves a node\'s other lines with its direct row, right behind it', () => {
    const others = otherLinesOf(base, base[0])
    expect(others).toEqual(['1.a'])
    const next = applyIntent(base, { moved: ['1.direct', ...others], after: '2.direct' })
    expect(ids(next)).toEqual(['sep:1', '2.direct', '1.direct', '1.a', '2.b'])
  })
  it('cannot be repeated once a row it names is gone, or after itself', () => {
    expect(applyIntent(base.slice(1), { moved: ['1.direct'], after: null })).toBeNull()
    expect(applyIntent(base.filter(r => rowId(r) !== '2.direct'), { moved: ['1.direct'], after: '2.direct' })).toBeNull()
    expect(applyIntent(base, { moved: ['1.direct'], after: '1.direct' })).toBeNull()
  })
  it('knows a no-op', () => {
    expect(isNoop(base, { moved: ['1.a'], after: '1.direct' })).toBe(true)
    expect(isNoop(base, { moved: ['1.a'], after: '2.direct' })).toBe(false)
  })
  it('names what another administrator changed: added, removed and how many moved', () => {
    const current = [base[3], base[0], line(3), base[1], base[2]]
    const d = diffOrders(base, current)
    expect(d.added.map(rowId)).toEqual(['3.direct'])
    expect(d.removed.map(rowId)).toEqual(['2.b'])
    expect(d.moved).toBe(1)
  })
})
