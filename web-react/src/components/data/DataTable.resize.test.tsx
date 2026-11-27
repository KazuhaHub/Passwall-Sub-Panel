// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from './DataTable'
import { columnWidthsKey } from './columnWidths'

// jsdom has no layout: every header cell is 200px wide (the "fitted" width), the frame is 700px.
const FITTED = 200
const FRAME = 800
beforeEach(() => {
  localStorage.clear()
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const w = this.tagName === 'TH' ? FITTED : this.hasAttribute('data-slot') && this.getAttribute('data-slot') === 'data-table' ? FRAME : this.classList.contains('group/frame') ? FRAME : 0
    return { top: 0, left: 0, right: w, bottom: 0, width: w, height: 0, x: 0, y: 0, toJSON() {} } as DOMRect
  })
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })

interface Row { id: string; name: string; status: string; note: string }
const rows: Row[] = Array.from({ length: 4 }, (_, i) => ({ id: `r${i}`, name: `Row ${i}`, status: 'Proxy service', note: 'n' }))
const columns: ColumnDef<Row>[] = [
  { accessorKey: 'name', header: 'User', meta: { hideable: false } },
  { accessorKey: 'status', header: 'Status' },
  { accessorKey: 'note', header: 'Note' },
  { id: 'actions', header: '', enableSorting: false, cell: () => 'x', meta: { hideable: false, label: 'Actions' } },
]
const mount = (props: { tableId?: string; columnMenu?: boolean } = {}) =>
  render(<DataTable<Row> tableId={props.tableId ?? ('tableId' in props ? undefined : 't1')} columnMenu={props.columnMenu} data={rows} columns={columns} getRowId={r => r.id} aria-label="T" />)

const handle = (name: string) => screen.getByRole('separator', { name: `Resize column ${name}` })
const colWidths = () => [...document.querySelectorAll<HTMLElement>('col')].map(c => c.style.width)
const stored = () => { const raw = localStorage.getItem(columnWidthsKey('t1')); return raw ? JSON.parse(raw) : null }

function drag(el: HTMLElement, from: number, to: number) {
  fireEvent.pointerDown(el, { clientX: from, button: 0, pointerId: 1 })
  act(() => { window.dispatchEvent(Object.assign(new Event('pointermove'), { clientX: to, pointerId: 1 })) })
  act(() => { window.dispatchEvent(Object.assign(new Event('pointerup'), { clientX: to, pointerId: 1 })) })
}

describe('column resizing', () => {
  it('puts a labelled, focusable handle on every data column but not on the actions column', () => {
    mount()
    for (const n of ['User', 'Status', 'Note']) {
      const h = handle(n)
      expect(h.getAttribute('aria-orientation')).toBe('vertical')
      expect(h.tabIndex).toBe(0)
    }
    expect(screen.getAllByRole('separator')).toHaveLength(3)
  })

  it('leaves the layout to the browser until someone resizes: no colgroup, no fixed layout', () => {
    mount()
    expect(document.querySelector('col')).toBeNull()
    expect(screen.getByRole('table').style.tableLayout).toBe('')
  })

  it('dragging the handle sets that column to start width plus the pointer distance and keeps the others fitted', () => {
    mount()
    drag(handle('Status'), 400, 460)
    // Nothing is squeezed: the table grows past the frame (the frame scrolls) rather than cutting the others short.
    expect(colWidths().slice(0, 3)).toEqual(['200px', '260px', '200px'])
    expect(screen.getByRole('table').style.tableLayout).toBe('fixed')
    expect(screen.getByRole('table').style.width).toBe('860px')
  })

  it('never goes below the minimum width', () => {
    mount()
    drag(handle('Status'), 400, 0)
    expect(parseInt(colWidths()[1])).toBe(64)
  })

  it('moves by 16px with the arrow keys, 64px with Shift, and reports the width to assistive technology', () => {
    mount()
    const h = handle('Status')
    fireEvent.keyDown(h, { key: 'ArrowRight' })
    expect(colWidths()[1]).toBe('216px')
    expect(h.getAttribute('aria-valuenow')).toBe('216')
    fireEvent.keyDown(h, { key: 'ArrowLeft', shiftKey: true })
    expect(colWidths()[1]).toBe('152px')
    expect(h.getAttribute('aria-valuemin')).toBe('64')
  })

  it('saves the width under the table id and column id, and restores it after a remount', () => {
    const { unmount } = mount()
    drag(handle('Status'), 400, 450)
    expect(localStorage.getItem('psp.colWidths.v1.t1')).not.toBeNull()
    expect(stored()).toEqual({ v: 1, w: { status: 250 } })
    unmount()
    mount()
    expect(colWidths().slice(0, 3)).toEqual(['200px', '250px', '200px'])
  })

  it('keeps tables apart: another table id does not see the widths', () => {
    mount()
    drag(handle('Status'), 400, 450)
    cleanup()
    mount({ tableId: 'other' })
    expect(document.querySelector('col')).toBeNull()
  })

  it('double-click on the handle returns that column to its fitted width and forgets it', () => {
    mount()
    drag(handle('Status'), 400, 450)
    drag(handle('Note'), 600, 640)
    fireEvent.doubleClick(handle('Status'))
    expect(stored()).toEqual({ v: 1, w: { note: 240 } })
    expect(colWidths().slice(0, 3)).toEqual(['200px', '200px', '240px'])
  })

  it('"Reset column widths" in the Columns menu clears the table and goes back to fitted layout', () => {
    mount()
    drag(handle('Status'), 400, 450)
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Reset column widths' }))
    expect(localStorage.getItem('psp.colWidths.v1.t1')).toBeNull()
    expect(document.querySelector('col')).toBeNull()
    expect(screen.getByRole('table').style.tableLayout).toBe('')
  })

  it('offers the reset only when there is something to reset', () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }))
    expect(screen.getByRole('menuitem', { name: 'Reset column widths' }).getAttribute('aria-disabled')).toBe('true')
  })

  it('still hides and shows columns, and a hidden column keeps its saved width', () => {
    mount()
    drag(handle('Status'), 400, 450)
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Status' }))
    expect(screen.queryByRole('columnheader', { name: /Status/ })).toBeNull()
    expect(stored()).toEqual({ v: 1, w: { status: 250 } })
  })

  it('renders and resizes when storage throws on every call', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('denied') })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('denied') })
    const g = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get() { throw new Error('denied') } })
    try {
      mount()
      expect(screen.getAllByRole('row')).toHaveLength(5)
      drag(handle('Status'), 400, 450)
      expect(colWidths()[1]).toBe('250px')
    } finally { if (g) Object.defineProperty(globalThis, 'localStorage', g) }
  })

  it('ignores a damaged saved value', () => {
    localStorage.setItem(columnWidthsKey('t1'), '{"v":1,"w":{"status":"wide","note":-5,"name":NaN}')
    mount()
    expect(document.querySelector('col')).toBeNull()
    localStorage.setItem(columnWidthsKey('t1'), JSON.stringify({ v: 1, w: { status: 'wide', note: -5, name: 300 } }))
    cleanup()
    mount()
    expect(colWidths().slice(0, 3)).toEqual(['300px', '200px', '200px'])
  })

  it('works without a table id (this visit only) and without a column menu', () => {
    mount({ tableId: undefined, columnMenu: false })
    drag(handle('Status'), 400, 450)
    expect(colWidths()[1]).toBe('250px')
    expect(localStorage.length).toBe(0)
  })

  it('shrinking a column hands the room to the last one, so the table still fills its frame', () => {
    mount()
    drag(handle('User'), 200, 140)
    // 800 frame: User 140 + Status 200 + (elastic) Note + actions 200 -> Note takes the 60px freed
    expect(colWidths()).toEqual(['140px', '200px', '260px', '200px'])
    expect(screen.getByRole('table').style.width).toBe('800px')
  })

  it('sizes the header cell and the body cells together (one colgroup drives both)', () => {
    mount()
    drag(handle('User'), 200, 300)
    const first = screen.getAllByRole('row')[1]
    expect(within(first).getAllByRole('cell')).toHaveLength(4)
    expect(document.querySelectorAll('col')).toHaveLength(4)
  })
})
