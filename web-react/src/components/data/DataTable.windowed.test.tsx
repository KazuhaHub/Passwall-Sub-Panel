// @vitest-environment jsdom
import '@/test/initI18n'
import { createRef, useState } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from './DataTable'
import type { DataTableHandle } from './dataTableTypes'

// jsdom has no layout, so a fixed one is faked: rows ROW px high in a body that
// starts BODY_TOP px below the top of the page, a HEAD px header, an 800px viewport.
const ROW = 40
const HEAD = 40
const BODY_TOP = 200
const VIEWPORT = 800
let scrollY = 0
let thW = 120

const box = (top: number, height: number, width = 600) => ({ top, bottom: top + height, height, left: 0, right: width, width, x: 0, y: top, toJSON() {} }) as DOMRect

beforeEach(() => {
  scrollY = 0
  thW = 120
  vi.useFakeTimers({ toFake: ['requestAnimationFrame', 'cancelAnimationFrame'] })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: VIEWPORT })
  Object.defineProperty(window, 'scrollY', { configurable: true, get: () => scrollY })
  window.scrollTo = ((_x: number, y: number) => { scrollY = y }) as typeof window.scrollTo
  // The bounding box (375) includes a scrollbar; the width to plan with is clientWidth (360).
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) { return this.classList.contains('group/frame') ? 360 : 0 })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.tagName === 'TBODY') return box(BODY_TOP - scrollY, 0)
    if (this.tagName === 'THEAD') return box(0, HEAD)
    if (this.tagName === 'TH') return box(0, HEAD, thW)
    if (this.classList.contains('group/frame')) return box(0, 0, 375)
    if (this.tagName === 'TR' && this.hasAttribute('data-row')) {
      const i = Number(this.getAttribute('aria-rowindex')) - 2
      return box(BODY_TOP - scrollY + i * ROW, ROW)
    }
    return box(0, 0)
  })
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers() })

interface Row { id: string; name: string }
const mk = (n: number): Row[] => Array.from({ length: n }, (_, i) => ({ id: `r${i + 1}`, name: `Line ${i + 1}` }))
const columns: ColumnDef<Row>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }, { accessorKey: 'id', header: 'Id', enableSorting: false }]
const BIG = mk(5000)

const scrollPageTo = (y: number) => {
  scrollY = y
  act(() => { window.dispatchEvent(new Event('scroll')); vi.advanceTimersByTime(20) })
}
const drawn = () => [...document.querySelectorAll<HTMLElement>('tr[data-row]')]
const drawnIds = () => drawn().map(r => r.getAttribute('data-id'))
const spacerPx = () => [...document.querySelectorAll<HTMLElement>('tr[data-spacer]')].reduce((s, r) => s + parseFloat(r.style.height), 0)

interface Extra { tableId?: string; maxHeight?: string | null; onRowOpen?: (r: Row) => void; selectable?: boolean; handleRef?: React.Ref<DataTableHandle> }
function mount(extra: Extra = {}, data: Row[] = BIG) {
  const onReorder = vi.fn()
  const utils = render(
    <DataTable
      data={data} columns={columns} getRowId={r => r.id} rowLabel={r => r.name} aria-label="Lines"
      pagination={false} columnMenu={false} onReorder={onReorder} {...extra}
    />,
  )
  return { onReorder, ...utils }
}

describe('windowed rendering', () => {
  it('draws a bounded number of rows for 5 000 and keeps the table semantics', () => {
    mount()
    expect(drawn().length).toBeGreaterThan(10)
    expect(drawn().length).toBeLessThan(80)
    const table = screen.getByRole('table')
    // every row of the list plus the header, though most are not drawn
    expect(table.getAttribute('aria-rowcount')).toBe('5001')
    expect(table.querySelector('thead tr')?.getAttribute('aria-rowindex')).toBe('1')
    expect(drawn()[0].getAttribute('aria-rowindex')).toBe('2')
    expect(drawn()[3].getAttribute('aria-rowindex')).toBe('5')
  })

  it('does not window 200 rows or fewer, and does not window a paginated table', () => {
    mount({}, mk(200))
    expect(drawn()).toHaveLength(200)
    expect(screen.getByRole('table').hasAttribute('aria-rowcount')).toBe(false)
    cleanup()
    render(<DataTable data={BIG} columns={columns} getRowId={r => r.id} aria-label="Lines" columnMenu={false} />)
    expect(drawn()).toHaveLength(25)
    expect(screen.getByRole('table').hasAttribute('aria-rowcount')).toBe(false)
  })

  it('follows the page scroll: the rows near the viewport are drawn and the rest is blank space', () => {
    mount()
    // viewport top at content y 40 000 - 200 = 39 800 inside the body: row 995
    scrollPageTo(40000)
    const ids = drawnIds()
    expect(ids).toContain('r1001')
    expect(ids).not.toContain('r1')
    expect(drawn().length).toBeLessThan(80)
    // drawn rows plus spacers always add up to the full list
    expect(drawn().length * ROW + spacerPx()).toBe(5000 * ROW)
    // a drawn row says where it really is: id rN is index N-1, so aria-rowindex N+1
    for (const r of drawn()) expect(Number(r.getAttribute('aria-rowindex'))).toBe(Number(r.getAttribute('data-id')!.slice(1)) + 1)
  })

  it('keeps the first rows drawn while the table is still below the fold', () => {
    mount()
    expect(drawnIds()[0]).toBe('r1')
    expect(spacerPx() + drawn().length * ROW).toBe(5000 * ROW)
  })

  it('sticks the header below the shell bar and leaves the page as the scroller', () => {
    mount()
    const head = document.querySelector('thead') as HTMLElement
    expect(head.style.top).toBe('var(--shell-bar-h, 0px)')
    const frame = screen.getByRole('table').parentElement as HTMLElement
    expect(frame.style.maxHeight).toBe('')
    expect(frame.className).not.toContain('overflow-auto')
  })

  it('uses its own box as the scroller when it is given a height', () => {
    mount({ maxHeight: '300px' })
    const frame = screen.getByRole('table').parentElement as HTMLElement
    expect(frame.style.maxHeight).toBe('300px')
    expect((document.querySelector('thead') as HTMLElement).style.top).toBe('')
  })

  it('freezes the column widths so they do not change as rows enter and leave', () => {
    mount()
    const table = screen.getByRole('table')
    expect(table.style.tableLayout).toBe('fixed')
    const cols = [...table.querySelectorAll('col')].map(c => c.style.width)
    // drag handle column, then the two data columns, each measured 120px
    expect(cols.length).toBeGreaterThanOrEqual(3)
    expect(cols.every(w => w.endsWith('px'))).toBe(true)
    scrollPageTo(40000)
    expect([...table.querySelectorAll('col')].map(c => c.style.width)).toEqual(cols)
  })
})

describe('windowed interaction', () => {
  it('selects a drawn row and keeps the selection of rows that have scrolled away', () => {
    const onSelectionChange = vi.fn()
    function Page() {
      const [sel, setSel] = useState<Record<string, boolean>>({})
      return <DataTable data={BIG} columns={columns} getRowId={r => r.id} rowLabel={r => r.name} aria-label="Lines" pagination={false} columnMenu={false} selectable selection={sel} onSelectionChange={n => { onSelectionChange(n); setSel(n) }} />
    }
    render(<Page />)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Line 2' }))
    expect(onSelectionChange).toHaveBeenLastCalledWith({ r2: true })
    scrollPageTo(40000)
    expect(drawnIds()).not.toContain('r2')
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Line 1001' }))
    expect(onSelectionChange).toHaveBeenLastCalledWith({ r2: true, r1001: true })
    // the bar counts every selected id, drawn or not
    expect(document.body.textContent).toContain('2')
  })

  it('opens a drawn row on click', () => {
    const onRowOpen = vi.fn()
    mount({ onRowOpen })
    fireEvent.click(document.querySelector('tr[data-id="r3"]')!)
    expect(onRowOpen).toHaveBeenCalledWith({ id: 'r3', name: 'Line 3' })
  })

  it('keeps the focused row drawn after it scrolls out of the window, and it is still the tab stop', () => {
    mount({ onRowOpen: () => {} })
    const row = document.querySelector<HTMLElement>('tr[data-id="r5"]')!
    act(() => { row.focus() })
    expect(document.activeElement).toBe(row)
    scrollPageTo(40000)
    const again = document.querySelector<HTMLElement>('tr[data-id="r5"]')
    expect(again).not.toBeNull()
    expect(document.activeElement).toBe(again)
    // the only element in the table's tab order, wherever it is
    expect(document.querySelectorAll('tr[data-row][tabindex="0"]')).toHaveLength(1)
    expect(again!.getAttribute('tabindex')).toBe('0')
    expect(again!.getAttribute('aria-rowindex')).toBe('6')
    // and it sits in the right place among the spacers: rows plus blank space still add up
    expect(drawn().length * ROW + spacerPx()).toBe(5000 * ROW)
    // scrolling back draws it in the normal window; focus is still there
    scrollPageTo(0)
    expect(document.activeElement).toBe(document.querySelector('tr[data-id="r5"]'))
  })

  it('keeps the row holding a focused control drawn too', () => {
    mount({ selectable: true })
    const box = screen.getByRole('checkbox', { name: 'Select Line 4' })
    act(() => { box.focus() })
    scrollPageTo(40000)
    expect(document.querySelector('tr[data-id="r4"]')).not.toBeNull()
  })

  it('moves focus with the arrows, Home and End across rows that are not drawn', () => {
    mount({ onRowOpen: () => {} })
    const row = document.querySelector<HTMLElement>('tr[data-id="r1"]')!
    act(() => { row.focus() })
    fireEvent.keyDown(row, { key: 'ArrowDown' })
    expect(document.activeElement?.getAttribute('data-id')).toBe('r2')
    fireEvent.keyDown(document.activeElement!, { key: 'End' })
    act(() => { vi.advanceTimersByTime(20) })
    expect(document.activeElement?.getAttribute('data-id')).toBe('r5000')
    expect(scrollY).toBeGreaterThan(0)
    fireEvent.keyDown(document.activeElement!, { key: 'Home' })
    act(() => { vi.advanceTimersByTime(20) })
    expect(document.activeElement?.getAttribute('data-id')).toBe('r1')
  })

  it('scrolls a row into view on request, for a search hit or after a move', () => {
    const handle = createRef<DataTableHandle>()
    mount({ handleRef: handle })
    let ok = false
    act(() => { ok = handle.current!.scrollToRow('r3000', { align: 'center' }) })
    expect(ok).toBe(true)
    // row 3000 has index 2999: top at 200 + 2999*40 in the page; centred under the 40px header
    const rowTop = BODY_TOP + 2999 * ROW
    expect(scrollY).toBe(rowTop - HEAD - (VIEWPORT - HEAD - ROW) / 2)
    expect(drawnIds()).toContain('r3000')
    expect(handle.current!.scrollToRow('nope')).toBe(false)
  })

  it('leaves the scroll alone when the row is already visible', () => {
    const handle = createRef<DataTableHandle>()
    mount({ handleRef: handle })
    scrollPageTo(0)
    act(() => { handle.current!.scrollToRow('r10') })
    expect(scrollY).toBe(0)
  })
})

describe('windowed reordering', () => {
  const handle = (n: number) => screen.getByRole('button', { name: `Drag to reorder: Line ${n}` })
  const pointer = (type: string, y: number, target: EventTarget = window) =>
    act(() => { target.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY: y, clientX: 10 })) })

  it('drops over a row that was not drawn when the drag began: row 10 to row 4 000', () => {
    const { onReorder } = mount()
    pointer('pointerdown', BODY_TOP + 9 * ROW + 20, handle(10))
    // the page is scrolled while the pointer is held (the wheel, or auto-scroll)
    scrollPageTo(4000 * ROW - 300)
    const y = BODY_TOP - scrollY + 4000 * ROW + 5 // just past the middle of the row that is index 3999... slot 4000
    pointer('pointermove', y)
    // the slot is shown on the row now at that index
    expect(document.querySelector('tr[data-drop="before"]')?.getAttribute('data-id')).toBe('r4001')
    pointer('pointerup', y)
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'r10', beforeId: 'r4001' })
  })

  it('keeps the dragged row drawn while it is scrolled far away', () => {
    mount()
    pointer('pointerdown', BODY_TOP + 9 * ROW + 20, handle(10))
    scrollPageTo(40000)
    expect(document.querySelector('tr[data-id="r10"]')).not.toBeNull()
    // carried rows are marked for both motion modes (the look differs: lifted, or dimmed under reduced motion)
    expect(document.querySelector('tr[data-id="r10"]')?.getAttribute('data-moving')).toBe('true')
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  })

  it('computes the slot from geometry, not from row boxes: it is right even if row boxes are wrong', () => {
    const { onReorder } = mount()
    pointer('pointerdown', BODY_TOP + 20, handle(1))
    // rows 3-5 claim absurd positions; the slot must not care
    const spy = HTMLElement.prototype.getBoundingClientRect as unknown as ReturnType<typeof vi.fn>
    const real = spy.getMockImplementation()!
    spy.mockImplementation(function (this: HTMLElement) {
      if (this.tagName === 'TR' && this.hasAttribute('data-row')) return box(99999, 1)
      return (real as () => DOMRect).call(this)
    })
    const y = BODY_TOP + 2 * ROW + 30 // 110px into the body, past the middle (100) of the third row: slot 3
    pointer('pointermove', y)
    pointer('pointerup', y)
    // r1 now sits at index 2 of the others, directly before r4
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'r1', beforeId: 'r4' })
  })

  it('scrolls the page while the pointer is held near the bottom edge, faster the longer it is held', () => {
    vi.spyOn(performance, 'now').mockReturnValue(1000)
    mount()
    pointer('pointerdown', BODY_TOP + 20, handle(1))
    pointer('pointermove', VIEWPORT - 4)
    act(() => { vi.advanceTimersByTime(20) })
    const first = scrollY
    expect(first).toBeGreaterThan(0)
    act(() => { vi.advanceTimersByTime(20) })
    const stepEarly = scrollY - first
    vi.spyOn(performance, 'now').mockReturnValue(4000)
    const before = scrollY
    act(() => { vi.advanceTimersByTime(20) })
    expect(scrollY - before).toBeGreaterThan(stepEarly)
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  })

  it('does not scroll when the pointer is under the sticky header\'s edge zone only in the middle', () => {
    mount()
    pointer('pointerdown', BODY_TOP + 20, handle(1))
    pointer('pointermove', 400)
    act(() => { vi.advanceTimersByTime(100) })
    expect(scrollY).toBe(0)
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
  })
})

describe('keyboard carrying: Home, End, PageUp, PageDown', () => {
  const handle = (n: number) => screen.getByRole('button', { name: `Drag to reorder: Line ${n}` })
  const status = () => screen.getByRole('status').textContent

  it('jumps to the last and first slot and announces the position out of the total', () => {
    const { onReorder } = mount()
    const h = handle(3)
    act(() => { h.focus() })
    fireEvent.keyDown(h, { key: ' ' })
    expect(status()).toContain('position 3 of 5000')
    fireEvent.keyDown(h, { key: 'End' })
    expect(status()).toContain('position 5000 of 5000')
    fireEvent.keyDown(h, { key: 'Home' })
    expect(status()).toContain('position 1 of 5000')
    fireEvent.keyDown(h, { key: 'Enter' })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'r3', beforeId: 'r1' })
  })

  it('moves by one viewport of rows with PageDown and PageUp', () => {
    const { onReorder } = mount()
    const h = handle(3)
    act(() => { h.focus() })
    fireEvent.keyDown(h, { key: ' ' })
    // (800 viewport - 40 header) / 40 = 19 rows
    fireEvent.keyDown(h, { key: 'PageDown' })
    expect(status()).toContain('position 22 of 5000')
    fireEvent.keyDown(h, { key: 'PageUp' })
    fireEvent.keyDown(h, { key: 'PageUp' })
    expect(status()).toContain('position 1 of 5000')
    fireEvent.keyDown(h, { key: 'PageDown' })
    expect(status()).toContain('position 20 of 5000')
    fireEvent.keyDown(h, { key: 'Enter' })
    // r3 now at index 19; the row after it is the 21st original row
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'r3', beforeId: 'r21' })
  })

  it('scrolls the target slot into view while carrying, and keeps the carried row and its handle drawn', () => {
    mount()
    const h = handle(3)
    act(() => { h.focus() })
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: 'End' })
    act(() => { vi.advanceTimersByTime(20) })
    expect(scrollY).toBeGreaterThan(100000)
    expect(drawnIds()).toContain('r5000')
    expect(drawnIds()).toContain('r3')
    expect(document.activeElement).toBe(handle(3))
  })

  it('Escape still cancels and the arrows still work', () => {
    const { onReorder } = mount()
    const h = handle(3)
    act(() => { h.focus() })
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    expect(status()).toContain('position 4 of 5000')
    fireEvent.keyDown(h, { key: 'Escape' })
    expect(onReorder).not.toHaveBeenCalled()
    expect(status()).toContain('Cancelled')
  })

  it('also works in a table that is not windowed, with a page of 10 rows', () => {
    const { onReorder } = mount({}, mk(40))
    const h = handle(2)
    act(() => { h.focus() })
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: 'PageDown' })
    expect(status()).toContain('position 12 of 40')
    fireEvent.keyDown(h, { key: 'End' })
    expect(status()).toContain('position 40 of 40')
    fireEvent.keyDown(h, { key: 'Enter' })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'r2', beforeId: null })
  })
})

describe('windowed column widths', () => {
  const widths = () => [...document.querySelectorAll<HTMLElement>('col')].map(c => c.style.width)
  const resize = (name: string, from: number, to: number) => {
    const h = screen.getByRole('separator', { name: `Resize column ${name}` })
    fireEvent.pointerDown(h, { clientX: from, button: 0, pointerId: 1 })
    act(() => { window.dispatchEvent(Object.assign(new Event('pointermove'), { clientX: to })) })
    act(() => { window.dispatchEvent(Object.assign(new Event('pointerup'), { clientX: to })) })
  }

  it('measures once and holds the same widths while the rows scroll in and out', () => {
    mount({ tableId: 'win' })
    const first = widths()
    expect(first.length).toBeGreaterThan(0)
    for (const y of [5000, 40000, 120000, 0]) {
      scrollPageTo(y)
      expect(widths()).toEqual(first)
    }
  })

  it('keeps an admin-set width, and the fitted width of the others, across scrolling', () => {
    mount({ tableId: 'win' })
    resize('Name', 100, 60)
    const set = widths()
    // columns: drag handle, Name, Id. The frame is 360 wide, and Id (the last, elastic one) takes the 40px Name gave up.
    expect(set).toEqual(['120px', '80px', '120px', ''])
    scrollPageTo(60000)
    expect(widths()).toEqual(set)
    scrollPageTo(0)
    expect(widths()).toEqual(set)
  })

  it('is held inside its frame: a clipped table cannot be dragged wider than the page it is on', () => {
    mount({ tableId: 'win' })
    // The frame is 360 wide, the drag handle column takes 120 and the elastic Id column is not squeezed below its 120: Name stays at 120.
    resize('Name', 100, 2000)
    expect(widths()).toEqual(['120px', '120px', '120px', ''])
  })

  it('scales saved widths down to fit a clipped table, so nothing is out of reach, and leaves the saved values alone', () => {
    localStorage.setItem('psp.colWidths.v1.win', JSON.stringify({ v: 1, w: { name: 300 } }))
    mount({ tableId: 'win' })
    // 120 + 300 + 120 = 540 in a 360 frame: the saved width gives up the 180px that do not fit.
    expect(widths()).toEqual(['120px', '120px', '120px', ''])
    expect(JSON.parse(localStorage.getItem('psp.colWidths.v1.win')!)).toEqual({ v: 1, w: { name: 300 } })
  })

  it('shows and starts from the SHOWN width when saved widths were scaled down, and never writes the scaled value back unasked', () => {
    localStorage.setItem('psp.colWidths.v1.win', JSON.stringify({ v: 1, w: { name: 300 } }))
    mount({ tableId: 'win' })
    const h = screen.getByRole('separator', { name: 'Resize column Name' })
    expect(h.getAttribute('aria-valuenow')).toBe('120')
    expect(JSON.parse(localStorage.getItem('psp.colWidths.v1.win')!).w.name).toBe(300)
    fireEvent.keyDown(h, { key: 'ArrowLeft' })
    expect(widths()[1]).toBe('104px')
  })

  it('a poll that changes an off-screen row does not refit the windowed table, but a different set of rows does', () => {
    const el = (data: Row[]) => <DataTable data={data} columns={columns} getRowId={r => r.id} aria-label="Lines" pagination={false} columnMenu={false} tableId="win" />
    const { rerender } = render(el(BIG))
    const first = widths()
    thW = 200
    rerender(el(BIG.map((r, i) => (i === 3000 ? { ...r, name: 'changed' } : { ...r }))))
    expect(widths()).toEqual(first)
    rerender(el(BIG.slice(0, 4000)))
    expect(widths()).not.toEqual(first)
  })
})
