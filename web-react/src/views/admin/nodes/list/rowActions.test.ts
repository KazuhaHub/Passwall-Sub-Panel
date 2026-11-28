import { describe, expect, it, vi } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { rowMenuItems, type RowActionCaps, type RowActionHandlers } from './rowActions'

const t = ((k: string) => k) as never
const row = (line_id: string, extra: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: 7, line_id, name: 'n', enabled: true, position: 1, positioned: true, landing: { enabled: true, panel_id: 3, name: 'L' } as never, ...extra,
})
const caps = (extra: Partial<RowActionCaps> = {}): RowActionCaps => ({ admin: true, enable: true, updateInbound: true, recreate: true, remove: true, ...extra })
const handlers = (): RowActionHandlers => ({
  editLine: vi.fn(), moveTo: vi.fn(), toggleNode: vi.fn(), recreateInbound: vi.fn(), removeFromPanel: vi.fn(), deleteNode: vi.fn(),
  editSeparator: vi.fn(), deleteSeparator: vi.fn(), toggleLine: vi.fn(), deleteLine: vi.fn(), addLine: vi.fn(), editMultiplier: vi.fn(),
})
const ids = (r: OrderRow, c = caps()) => rowMenuItems({ row: r, caps: c, handlers: handlers(), t }).map(i => i.id)

describe('a line row menu', () => {
  it('offers the core actions on a direct row, its edit being the full node editor, with Add relay line, in order', () => {
    expect(ids(row('direct'))).toEqual(['edit-node', 'multiplier', 'add-line', 'toggle-node', 'move'])
  })
  it('offers the line and the node switch on a relay row, and delete last', () => {
    expect(ids(row('r1'))).toEqual(['edit-line', 'multiplier', 'toggle-line', 'toggle-node', 'move', 'edit-node', 'delete-line'])
  })
  it('does not offer the multiplier for a row whose line cannot be addressed', () => {
    expect(ids(row('direct', { landing: { enabled: true, name: 'L' } as never }))).not.toContain('multiplier')
    expect(ids(row('r1', { node_id: undefined }))).not.toContain('multiplier')
  })
  it('does not offer the multiplier when no handler is provided', () => {
    const h = handlers()
    delete h.editMultiplier
    expect(rowMenuItems({ row: row('direct'), caps: caps(), handlers: h, t }).map(i => i.id)).not.toContain('multiplier')
  })
  it('wires the multiplier, add line and node switch to the handlers', () => {
    const h = handlers()
    const r = row('direct')
    const items = rowMenuItems({ row: r, caps: caps(), handlers: h, t })
    items.find(i => i.id === 'multiplier')?.onSelect?.()
    items.find(i => i.id === 'add-line')?.onSelect?.()
    items.find(i => i.id === 'toggle-node')?.onSelect?.()
    expect(h.editMultiplier).toHaveBeenCalledWith(r)
    expect(h.addLine).toHaveBeenCalledWith(r)
    expect(h.toggleNode).toHaveBeenCalledWith(r, false)
    expect(items.find(i => i.id === 'edit-node')?.to).toBe('/admin/nodes/7?tab=general')
  })
  it('turns the node switch into Enable on a disabled node, and disables it when the server cannot', () => {
    const items = rowMenuItems({ row: row('direct', { landing: { enabled: false } as never }), caps: caps({ enable: false }), handlers: handlers(), t })
    const sw = items.find(i => i.id === 'toggle-node')
    expect(sw?.label).toBe('nodes.action.enable_node')
    expect(sw?.disabled).toBe(true)
  })
  it('leaves an operator only the node switch', () => {
    expect(ids(row('direct'), caps({ admin: false }))).toEqual(['toggle-node'])
  })
  it('keeps the separator menu', () => {
    expect(ids({ kind: 'separator', id: 1, name: 's', enabled: true, position: 1, positioned: true })).toEqual(['edit', 'move', 'delete'])
  })
})
