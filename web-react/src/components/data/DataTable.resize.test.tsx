// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from './DataTable'
import { columnWidthsKey } from './columnWidths'

// jsdom has no layout: every header cell is 200px wide (the "fitted" width), the frame is 700px.
let FITTED = 200
const FRAME = 800
// The frame's clientWidth excludes a vertical scrollbar, the bounding box does not: they differ by 15 here.
let FRAME_CLIENT = FRAME - 15
// offsetWidth is the border box: a scrollbar appearing or leaving does not change it.
let FRAME_OFFSET = FRAME
let resizeCallbacks: (() => void)[] = []
beforeEach(() => {
  localStorage.clear()
  FITTED = 200
  FRAME_CLIENT = FRAME - 15
  FRAME_OFFSET = FRAME
  resizeCallbacks = []
  vi.stubGlobal('ResizeObserver', class { constructor(cb: () => void) { resizeCallbacks.push(cb) } observe() {} disconnect() {} unobserve() {} })
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) { return this.classList.contains('group/frame') ? FRAME_CLIENT : 0 })
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) { return this.classList.contains('group/frame') ? FRAME_OFFSET : 0 })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const w = this.tagName === 'TH' ? FITTED : this.hasAttribute('data-slot') && this.getAttribute('data-slot') === 'data-table' ? FRAME : this.classList.contains('group/frame') ? FRAME : 0
    return { top: 0, left: 0, right: w, bottom: 0, width: w, height: 0, x: 0, y: 0, toJSON() {} } as DOMRect
  })
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

interface Row { id: string; name: string; status: string; note: string }
const rows: Row[] = Array.from({ length: 4 }, (_, i) => ({ id: `r${i}`, name: `Row ${i}`, status: 'Proxy service', note: 'n' }))
const columns: ColumnDef<Row>[] = [
  { accessorKey: 'name', header: 'User', meta: { hideable: false } },
  { accessorKey: 'status', header: 'Status' },
  { accessorKey: 'note', header: 'Note' },
  { id: 'actions', header: '', enableSorting: false, cell: () => 'x', meta: { hideable: false, label: 'Actions' } },
]
const mount = (props: { tableId?: string; columnMenu?: boolean; cols?: ColumnDef<Row>[]; data?: Row[] } = {}) =>
  render(<DataTable<Row> tableId={props.tableId ?? ('tableId' in props ? undefined : 't1')} columnMenu={props.columnMenu} data={props.data ?? rows} columns={props.cols ?? columns} getRowId={r => r.id} aria-label="T" />)

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
    // The actions column (last, no header text) is the one left without a width: it takes what the frame has left, at the right edge.
    expect(colWidths()).toEqual(['200px', '260px', '200px', ''])
    expect(screen.getByRole('table').style.tableLayout).toBe('fixed')
    expect(screen.getByRole('table').style.minWidth).toBe('860px')
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

  it('lets the last real column be made narrower, and it stays that width', () => {
    mount()
    drag(handle('Note'), 600, 560)
    expect(colWidths()).toEqual(['200px', '200px', '160px', ''])
    expect(screen.getByRole('table').style.minWidth).toBe('760px')
  })

  it('adds an empty trailing filler column when there is no actions column to take the leftover', () => {
    mount({ cols: columns.slice(0, 3) })
    drag(handle('Note'), 600, 560)
    expect(colWidths()).toEqual(['200px', '200px', '160px', ''])
    // A real, empty, hidden trailing cell in the header and in every row: a bare <col> paints nothing, so the header band
    // and the row backgrounds would stop at the last real column.
    expect(document.querySelectorAll('thead th')).toHaveLength(4)
    expect(document.querySelector('thead th:last-child')?.getAttribute('aria-hidden')).toBe('true')
    const bodyRows = [...document.querySelectorAll('tbody tr[data-row]')]
    expect(bodyRows).toHaveLength(4)
    for (const r of bodyRows) {
      expect(r.lastElementChild?.getAttribute('aria-hidden')).toBe('true')
      expect(r.children).toHaveLength(4)
    }
    expect(screen.getAllByRole('columnheader')).toHaveLength(3)
    expect(screen.getByRole('table').className).toContain('w-full')
  })

  it('has no filler cell before a width is set, nor when the actions column is the one that absorbs', () => {
    mount({ cols: columns.slice(0, 3) })
    expect(document.querySelectorAll('thead th')).toHaveLength(3)
    cleanup()
    mount()
    drag(handle('Status'), 400, 450)
    expect(document.querySelectorAll('thead th')).toHaveLength(4)
    expect(document.querySelectorAll('thead th[aria-hidden]')).toHaveLength(0)
  })

  it('a scrollbar appearing (clientWidth changes, the border box does not) is not a resize: nothing is measured again', () => {
    mount()
    drag(handle('Status'), 400, 450)
    FITTED = 260
    FRAME_CLIENT = FRAME - 30
    act(() => { resizeCallbacks.forEach(cb => cb()) })
    expect(colWidths()).toEqual(['200px', '250px', '200px', ''])
    FRAME_OFFSET = 500
    act(() => { resizeCallbacks.forEach(cb => cb()) })
    expect(colWidths()).toEqual(['260px', '250px', '260px', ''])
  })

  it('reports the fitted width as the handle value right after a Home reset', () => {
    mount()
    const h = handle('Status')
    h.focus()
    fireEvent.keyDown(h, { key: 'ArrowRight' })
    expect(h.getAttribute('aria-valuenow')).toBe('216')
    fireEvent.keyDown(h, { key: 'Home' })
    expect(h.getAttribute('aria-valuenow')).toBe('200')
  })

  it('sizes the header cell and the body cells together (one colgroup drives both)', () => {
    mount()
    drag(handle('User'), 200, 300)
    const first = screen.getAllByRole('row')[1]
    expect(within(first).getAllByRole('cell')).toHaveLength(4)
    expect(document.querySelectorAll('col')).toHaveLength(4)
  })

  it('Home on the handle resets that column, like a double click, and the handle says so', () => {
    mount()
    drag(handle('Status'), 400, 450)
    const h = handle('Status')
    const desc = document.getElementById(h.getAttribute('aria-describedby') ?? '')
    expect(desc?.textContent).toMatch(/Home/)
    fireEvent.keyDown(h, { key: 'Home' })
    expect(stored()).toBeNull()
  })

  it('reads the frame by clientWidth: a scrollbar must not count as room', () => {
    // bounding 800, client 785: a restore that scaled to the bounding width would leave the last column 15px out of reach
    localStorage.setItem(columnWidthsKey('t1'), JSON.stringify({ v: 1, w: { status: 250 } }))
    mount()
    expect(screen.getByRole('table').style.minWidth).toBe('850px')
  })

  it('measures again when the rows are replaced by a refetch (content may have grown)', () => {
    const { rerender } = mount()
    drag(handle('Status'), 400, 450)
    expect(colWidths()).toEqual(['200px', '250px', '200px', ''])
    FITTED = 260
    rerender(<DataTable<Row> tableId="t1" data={rows.map(r => ({ ...r }))} columns={columns} getRowId={r => r.id} aria-label="T" />)
    expect(colWidths()).toEqual(['260px', '250px', '260px', ''])
  })

  it('does not measure again for a re-render with the same rows', () => {
    const { rerender } = mount()
    drag(handle('Status'), 400, 450)
    FITTED = 260
    rerender(<DataTable<Row> tableId="t1" data={rows} columns={columns} getRowId={r => r.id} aria-label="T" />)
    expect(colWidths()).toEqual(['200px', '250px', '200px', ''])
  })

  it('applies saved widths once a table that was hidden when it first drew gets a width', () => {
    localStorage.setItem(columnWidthsKey('t1'), JSON.stringify({ v: 1, w: { status: 250 } }))
    FITTED = 0
    FRAME_CLIENT = 0
    FRAME_OFFSET = 0
    mount()
    expect(document.querySelector('col')).toBeNull()
    FITTED = 200
    FRAME_CLIENT = 800
    FRAME_OFFSET = 800
    act(() => { resizeCallbacks.forEach(cb => cb()) })
    expect(colWidths()).toEqual(['200px', '250px', '200px', ''])
  })
})

describe('the Columns button', () => {
  const noHide: ColumnDef<Row>[] = [
    { accessorKey: 'name', header: 'User', meta: { hideable: false } },
    { accessorKey: 'status', header: 'Status', meta: { hideable: false } },
  ]
  it('is not shown when there is nothing to hide and nothing to reset', () => {
    mount({ cols: noHide })
    expect(screen.queryByRole('button', { name: 'Columns' })).toBeNull()
  })
  it('is not offered on a table that switched the column list off (columnMenu false), even once a width is set: it resets from the handle', () => {
    mount({ columnMenu: false })
    drag(handle('Status'), 400, 450)
    expect(screen.queryByRole('button', { name: 'Columns' })).toBeNull()
    fireEvent.keyDown(handle('Status'), { key: 'Home' })
    expect(stored()).toBeNull()
  })
  it('stays on screen while the menu resets the widths, so the focused trigger is never unmounted', () => {
    mount()
    drag(handle('Status'), 400, 450)
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Reset column widths' }))
    expect(screen.getByRole('button', { name: 'Columns' })).toBeTruthy()
  })
})
