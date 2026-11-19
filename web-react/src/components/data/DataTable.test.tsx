// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from './DataTable'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
beforeEach(() => { localStorage.clear() })

interface Row { id: string; name: string; status: string; traffic: number; kids?: Row[] }
const mk = (n: number): Row[] => Array.from({ length: n }, (_, i) => ({
  id: `r${i + 1}`, name: `Node ${String(i + 1).padStart(2, '0')}`, status: i % 2 ? 'down' : 'up', traffic: (i * 37) % 11,
}))
const columns: ColumnDef<Row>[] = [
  { accessorKey: 'name', header: 'Name', meta: { hideable: false } },
  { accessorKey: 'status', header: 'Status', enableSorting: false },
  { accessorKey: 'traffic', header: 'Traffic' },
]
const names = () => screen.getAllByRole('row').slice(1).map(r => within(r).queryAllByRole('cell')[0]?.textContent)

describe('semantics', () => {
  it('is a real table with column headers, and only sortable headers are buttons with aria-sort', () => {
    render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} aria-label="Nodes" />)
    expect(screen.getByRole('table', { name: 'Nodes' })).toBeTruthy()
    expect(screen.getAllByRole('columnheader')).toHaveLength(3)
    expect(screen.getByRole('columnheader', { name: /Name/ }).getAttribute('aria-sort')).toBe('none')
    expect(screen.getByRole('columnheader', { name: /Status/ }).getAttribute('aria-sort')).toBeNull()
    expect(within(screen.getByRole('columnheader', { name: /Status/ })).queryByRole('button')).toBeNull()
    expect(within(screen.getByRole('columnheader', { name: /Name/ })).getByRole('button')).toBeTruthy()
  })
})

describe('client mode', () => {
  it('pages rows in memory and reports the range with the shared wording', () => {
    render(<DataTable data={mk(25)} columns={columns} getRowId={r => r.id} pageSize={10} />)
    expect(names()).toHaveLength(10)
    expect(screen.getByText('1–10 of 25')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    expect(screen.getByText('11–20 of 25')).toBeTruthy()
    expect(names()[0]).toBe('Node 11')
    fireEvent.click(screen.getByRole('button', { name: 'Last page' }))
    expect(names()).toHaveLength(5)
  })

  it('offers the page sizes the app already uses', () => {
    render(<DataTable data={mk(30)} columns={columns} getRowId={r => r.id} />)
    const select = screen.getByRole('combobox', { name: 'Rows per page' }) as HTMLSelectElement
    expect([...select.options].map(o => o.value)).toEqual(['10', '25', '50', '100'])
    expect(select.value).toBe('25')
    fireEvent.change(select, { target: { value: '10' } })
    expect(names()).toHaveLength(10)
  })

  it('sorts on header click: ascending, descending, then off, with aria-sort following', () => {
    render(<DataTable data={mk(5)} columns={columns} getRowId={r => r.id} />)
    const th = () => screen.getByRole('columnheader', { name: /Traffic/ })
    const before = names()
    fireEvent.click(within(th()).getByRole('button'))
    expect(th().getAttribute('aria-sort')).toBe('ascending')
    fireEvent.click(within(th()).getByRole('button'))
    expect(th().getAttribute('aria-sort')).toBe('descending')
    fireEvent.click(within(th()).getByRole('button'))
    expect(th().getAttribute('aria-sort')).toBe('none')
    expect(names()).toEqual(before)
  })

  it('actually reorders the rows when sorted', () => {
    const rows: Row[] = [{ id: 'a', name: 'B', status: 'up', traffic: 2 }, { id: 'b', name: 'A', status: 'up', traffic: 1 }]
    render(<DataTable data={rows} columns={columns} getRowId={r => r.id} />)
    fireEvent.click(within(screen.getByRole('columnheader', { name: /Name/ })).getByRole('button'))
    expect(names()).toEqual(['A', 'B'])
  })

  it('hides the footer and shows everything when pagination is off', () => {
    render(<DataTable data={mk(30)} columns={columns} getRowId={r => r.id} pagination={false} />)
    expect(names()).toHaveLength(30)
    expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull()
  })
})

describe('server mode', () => {
  it('shows the page it was given, with the total it was told', () => {
    render(<DataTable mode="server" data={mk(25)} total={95} page={2} pageSize={25} columns={columns} getRowId={r => r.id} />)
    expect(names()).toHaveLength(25)
    expect(screen.getByText('26–50 of 95')).toBeTruthy()
  })

  it('reports paging, page size and sorting to the caller and does not sort or slice itself', () => {
    const onPageChange = vi.fn()
    const onPageSizeChange = vi.fn()
    const onSortingChange = vi.fn()
    const data = mk(10)
    render(<DataTable
      mode="server" data={data} total={95} page={2} pageSize={10} columns={columns} getRowId={r => r.id}
      onPageChange={onPageChange} onPageSizeChange={onPageSizeChange} onSortingChange={onSortingChange}
    />)
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    expect(onPageChange).toHaveBeenCalledWith(3)
    fireEvent.click(screen.getByRole('button', { name: 'First page' }))
    expect(onPageChange).toHaveBeenLastCalledWith(1)
    fireEvent.change(screen.getByRole('combobox', { name: 'Rows per page' }), { target: { value: '50' } })
    expect(onPageSizeChange).toHaveBeenCalledWith(50)
    fireEvent.click(within(screen.getByRole('columnheader', { name: /Traffic/ })).getByRole('button'))
    expect(onSortingChange).toHaveBeenCalledWith([{ id: 'traffic', desc: false }])
    expect(names()).toEqual(data.map(r => r.name))
  })

  it('shows the sort the caller holds, and a controlled sort moves aria-sort', () => {
    render(<DataTable mode="server" data={mk(3)} total={3} sorting={[{ id: 'name', desc: true }]} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByRole('columnheader', { name: /Name/ }).getAttribute('aria-sort')).toBe('descending')
  })

  it('asks for the last page again when the total shrinks under the current page', () => {
    const onPageChange = vi.fn()
    render(<DataTable mode="server" data={[]} total={30} page={5} pageSize={25} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    expect(onPageChange).toHaveBeenCalledWith(2)
  })
})

describe('states', () => {
  it('shows skeleton rows while loading and marks the table busy', () => {
    const { container } = render(<DataTable data={[]} loading columns={columns} getRowId={r => r.id} />)
    expect(container.querySelectorAll('[data-skeleton-row]').length).toBeGreaterThanOrEqual(5)
    expect(screen.getByRole('table').getAttribute('aria-busy')).toBe('true')
  })

  it('keeps the rows on screen while refetching', () => {
    const { container } = render(<DataTable data={mk(3)} loading columns={columns} getRowId={r => r.id} />)
    expect(container.querySelectorAll('[data-skeleton-row]')).toHaveLength(0)
    expect(names()).toHaveLength(3)
    expect(screen.getByRole('table').getAttribute('aria-busy')).toBe('true')
  })

  it('says so when empty, and takes a custom empty state', () => {
    const { rerender } = render(<DataTable data={[]} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByText('No data')).toBeTruthy()
    rerender(<DataTable data={[]} columns={columns} getRowId={r => r.id} empty={<div>No nodes yet. Add one to start.</div>} />)
    expect(screen.getByText('No nodes yet. Add one to start.')).toBeTruthy()
  })

  it('shows an error with a retry instead of the rows', () => {
    const onRetry = vi.fn()
    render(<DataTable data={mk(3)} error columns={columns} getRowId={r => r.id} onRetry={onRetry} />)
    expect(screen.getByText('Could not load the data')).toBeTruthy()
    expect(screen.queryByText('Node 01')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('shows an error detail when given one', () => {
    render(<DataTable data={[]} error={new Error('502 from upstream')} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByText('502 from upstream')).toBeTruthy()
  })
})

describe('selection and the bulk bar', () => {
  function Harness({ onBulk }: { onBulk?: (ids: string[]) => void }) {
    const [sel, setSel] = useState<Record<string, boolean>>({})
    return (
      <DataTable
        data={mk(4)} columns={columns} getRowId={r => r.id} selectable selection={sel} onSelectionChange={setSel}
        rowLabel={r => r.name}
        bulkActions={({ selectedIds, clear }) => (<><button onClick={() => onBulk?.(selectedIds)}>Disable</button><button onClick={clear}>Done</button></>)}
      />
    )
  }

  it('has no bar until something is selected, and labels every checkbox', () => {
    render(<Harness />)
    expect(screen.queryByRole('region', { name: 'Bulk actions' })).toBeNull()
    expect(screen.getByRole('checkbox', { name: 'Select all rows on this page' })).toBeTruthy()
    expect(screen.getByRole('checkbox', { name: 'Select Node 02' })).toBeTruthy()
  })

  it('shows the bar with the count and the page\'s actions once rows are picked', () => {
    const onBulk = vi.fn()
    render(<Harness onBulk={onBulk} />)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Node 01' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Node 03' }))
    const bar = screen.getByRole('region', { name: 'Bulk actions' })
    expect(bar.textContent).toContain('2 selected')
    fireEvent.click(within(bar).getByRole('button', { name: 'Disable' }))
    expect(onBulk).toHaveBeenCalledWith(['r1', 'r3'])
  })

  it('selects and clears the whole page from the header', () => {
    render(<Harness />)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).toContain('4 selected')
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.queryByRole('region', { name: 'Bulk actions' })).toBeNull()
  })
})

describe('column menu', () => {
  it('hides and shows optional columns, and never lists a column marked not hideable', () => {
    const onVis = vi.fn()
    render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} onColumnVisibilityChange={onVis} />)
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }))
    expect(screen.queryByRole('menuitemcheckbox', { name: 'Name' })).toBeNull()
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Traffic' }))
    expect(onVis).toHaveBeenCalledWith(expect.objectContaining({ traffic: false }))
    expect(screen.queryByRole('columnheader', { name: /Traffic/ })).toBeNull()
  })

  it('starts with defaultHidden columns hidden', () => {
    const cols: ColumnDef<Row>[] = [...columns, { accessorKey: 'id', header: 'ID', meta: { defaultHidden: true } }]
    render(<DataTable data={mk(2)} columns={cols} getRowId={r => r.id} />)
    expect(screen.queryByRole('columnheader', { name: 'ID' })).toBeNull()
  })

  it('keeps defaultHidden columns hidden when the caller holds an empty visibility state', () => {
    const cols: ColumnDef<Row>[] = [...columns, { accessorKey: 'id', header: 'ID', meta: { defaultHidden: true } }]
    const { rerender } = render(<DataTable data={mk(2)} columns={cols} getRowId={r => r.id} columnVisibility={{}} onColumnVisibilityChange={() => {}} />)
    expect(screen.queryByRole('columnheader', { name: 'ID' })).toBeNull()
    rerender(<DataTable data={mk(2)} columns={cols} getRowId={r => r.id} columnVisibility={{ id: true }} onColumnVisibilityChange={() => {}} />)
    expect(screen.getByRole('columnheader', { name: 'ID' })).toBeTruthy()
  })

  it('renders the toolbar first and the column menu after it', () => {
    render(<DataTable data={mk(2)} columns={columns} getRowId={r => r.id} toolbar={<input aria-label="find" />} />)
    const find = screen.getByLabelText('find')
    const cols = screen.getByRole('button', { name: 'Columns' })
    expect(find.compareDocumentPosition(cols) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

describe('opening and moving between rows', () => {
  const setup = (extra: Partial<React.ComponentProps<typeof DataTable<Row>>> = {}) => {
    const onRowOpen = vi.fn()
    const cols: ColumnDef<Row>[] = [
      ...columns,
      { id: 'act', header: '', cell: () => <><button>Edit</button><input aria-label="note" /></>, enableSorting: false },
    ]
    render(<DataTable data={mk(4)} columns={cols} getRowId={r => r.id} onRowOpen={onRowOpen} pagination={false} {...extra} />)
    return { onRowOpen, rows: () => screen.getAllByRole('row').slice(1) }
  }

  it('opens a row on click', () => {
    const { onRowOpen, rows } = setup()
    fireEvent.click(within(rows()[1]).getByText('Node 02'))
    expect(onRowOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'r2' }))
  })

  it('does not open a row when a control inside it is clicked', () => {
    const { onRowOpen, rows } = setup()
    fireEvent.click(within(rows()[0]).getByRole('button', { name: 'Edit' }))
    fireEvent.click(within(rows()[0]).getByLabelText('note'))
    expect(onRowOpen).not.toHaveBeenCalled()
  })

  it('opens the focused row on Enter, and the first row is the one tab stop', () => {
    const { onRowOpen, rows } = setup()
    expect(rows().filter(r => r.tabIndex === 0)).toHaveLength(1)
    expect(rows()[0].tabIndex).toBe(0)
    rows()[0].focus()
    fireEvent.keyDown(rows()[0], { key: 'Enter' })
    expect(onRowOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'r1' }))
  })

  it('moves focus with the arrow keys, Home and End', () => {
    const { rows } = setup()
    rows()[0].focus()
    fireEvent.keyDown(rows()[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(rows()[1])
    fireEvent.keyDown(rows()[1], { key: 'End' })
    expect(document.activeElement).toBe(rows()[3])
    fireEvent.keyDown(rows()[3], { key: 'ArrowUp' })
    expect(document.activeElement).toBe(rows()[2])
    fireEvent.keyDown(rows()[2], { key: 'Home' })
    expect(document.activeElement).toBe(rows()[0])
  })

  it('leaves the keys alone while typing in an input inside a row', () => {
    const { onRowOpen, rows } = setup()
    const input = within(rows()[0]).getByLabelText('note')
    input.focus()
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(document.activeElement).toBe(input)
    expect(onRowOpen).not.toHaveBeenCalled()
  })

  it('marks the open row', () => {
    const { rows } = setup({ activeRowId: 'r2' })
    expect(rows()[1].getAttribute('data-active')).toBe('true')
    expect(rows()[0].getAttribute('data-active')).toBeNull()
  })

  it('toggles selection with Space on a focused row when selectable', () => {
    const { rows } = setup({ selectable: true })
    rows()[2].focus()
    fireEvent.keyDown(rows()[2], { key: ' ' })
    expect(rows()[2].getAttribute('data-state')).toBe('selected')
  })
})

describe('reordering', () => {
  const rows = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }, { id: 'c', name: 'C' }, { id: 'd', name: 'D' }] as Row[]
  const cols: ColumnDef<Row>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }]
  const handle = (name: string) => screen.getByRole('button', { name: `Drag to reorder: ${name}` })
  const mount = (onReorder = vi.fn()) => {
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={onReorder} pagination={false} columnMenu={false} />)
    return onReorder
  }

  it('gives every row a labelled handle that explains the keys', () => {
    mount()
    expect(screen.getAllByRole('button', { name: /Drag to reorder/ })).toHaveLength(4)
    expect(handle('A').getAttribute('aria-describedby')).toBeTruthy()
    expect(handle('A').getAttribute('aria-roledescription')).toBe('sortable')
  })

  it('moves a row with the keyboard: Space to pick up, arrows to move, Space to drop', () => {
    const onReorder = mount()
    const h = handle('A')
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    expect(screen.getByRole('status').textContent).toContain('Picked up A')
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    expect(screen.getByRole('status').textContent).toContain('position 3 of 4')
    fireEvent.keyDown(h, { key: ' ' })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'a', beforeId: 'd' })
    expect(screen.getByRole('status').textContent).toContain('dropped at position 3')
  })

  it('reports a move to the end as beforeId null', () => {
    const onReorder = mount()
    const h = handle('B')
    h.focus()
    fireEvent.keyDown(h, { key: 'Enter' })
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    fireEvent.keyDown(h, { key: 'Enter' })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'b', beforeId: null })
  })

  it('cancels with Escape and sends nothing', () => {
    const onReorder = mount()
    const h = handle('C')
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: 'ArrowUp' })
    fireEvent.keyDown(h, { key: 'Escape' })
    expect(onReorder).not.toHaveBeenCalled()
    expect(screen.getByRole('status').textContent).toContain('Cancelled')
  })

  it('sends nothing when it is dropped where it was', () => {
    const onReorder = mount()
    const h = handle('C')
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: ' ' })
    expect(onReorder).not.toHaveBeenCalled()
  })

  it('drags with the pointer: the row lands before the one under the pointer', () => {
    const onReorder = mount()
    // jsdom has no layout: give each body row a 40px box.
    const trs = screen.getAllByRole('row').slice(1)
    trs.forEach((tr, i) => {
      tr.getBoundingClientRect = () => ({ top: i * 40, bottom: i * 40 + 40, height: 40, left: 0, right: 100, width: 100, x: 0, y: i * 40, toJSON() {} }) as DOMRect
    })
    const h = handle('A')
    const down = new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 20 })
    act(() => { h.dispatchEvent(down) })
    act(() => { window.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientY: 125 })) })
    expect(trs[3].getAttribute('data-drop')).toBe('before')
    act(() => { window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientY: 125 })) })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'a', beforeId: 'd' })
  })

  it('a pointer drag that ends where it began sends nothing, and Escape cancels one in flight', () => {
    const onReorder = mount()
    const trs = screen.getAllByRole('row').slice(1)
    trs.forEach((tr, i) => {
      tr.getBoundingClientRect = () => ({ top: i * 40, bottom: i * 40 + 40, height: 40, left: 0, right: 100, width: 100, x: 0, y: i * 40, toJSON() {} }) as DOMRect
    })
    const h = handle('B')
    act(() => { h.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 60 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientY: 60 })) })
    expect(onReorder).not.toHaveBeenCalled()
    act(() => { h.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 60 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientY: 150 })) })
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientY: 150 })) })
    expect(onReorder).not.toHaveBeenCalled()
  })

  it('turns the handles off while a column sort is on, because the order shown is not the stored one', () => {
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} sorting={[{ id: 'name', desc: false }]} pagination={false} />)
    expect((screen.getByRole('button', { name: /Drag to reorder: A/ }) as HTMLButtonElement).disabled).toBe(true)
  })
})

describe('tree rows', () => {
  const tree: Row[] = [
    { id: 'p1', name: 'Irvine Home', status: 'up', traffic: 0, kids: [
      { id: 'c1', name: 'HiNet line', status: 'up', traffic: 1 },
      { id: 'c2', name: 'Direct line', status: 'down', traffic: 2 },
    ] },
    { id: 'p2', name: 'Tokyo', status: 'up', traffic: 0 },
  ]
  const mount = () => render(<DataTable data={tree} columns={columns} getRowId={r => r.id} getSubRows={r => r.kids} rowLabel={r => r.name} columnMenu={false} />)

  it('shows parents collapsed, with an expander only where there are children', () => {
    mount()
    expect(names()).toEqual(['Irvine Home', 'Tokyo'])
    expect(screen.getByRole('button', { name: 'Expand Irvine Home' }).getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByRole('button', { name: 'Expand Tokyo' })).toBeNull()
  })

  it('expands to indented children and collapses again', () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: 'Expand Irvine Home' }))
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows.map(r => r.getAttribute('data-depth'))).toEqual(['0', '1', '1', '0'])
    fireEvent.click(screen.getByRole('button', { name: 'Collapse Irvine Home' }))
    expect(names()).toHaveLength(2)
  })

  it('expands and collapses from the keyboard with Right and Left', () => {
    mount()
    const first = screen.getAllByRole('row')[1]
    first.focus()
    fireEvent.keyDown(first, { key: 'ArrowRight' })
    expect(screen.getAllByRole('row')).toHaveLength(5)
    fireEvent.keyDown(first, { key: 'ArrowLeft' })
    expect(screen.getAllByRole('row')).toHaveLength(3)
  })

  it('shows children when pagination is off too', () => {
    render(<DataTable data={tree} columns={columns} getRowId={r => r.id} getSubRows={r => r.kids} defaultExpanded pagination={false} columnMenu={false} />)
    expect(screen.getAllByRole('row')).toHaveLength(5)
  })

  it('can start expanded', () => {
    render(<DataTable data={tree} columns={columns} getRowId={r => r.id} getSubRows={r => r.kids} defaultExpanded columnMenu={false} />)
    expect(screen.getAllByRole('row')).toHaveLength(5)
  })

  it('keeps children with their parent when paging', () => {
    render(<DataTable data={tree} columns={columns} getRowId={r => r.id} getSubRows={r => r.kids} defaultExpanded pageSize={10} columnMenu={false} />)
    expect(screen.getByText('1–2 of 2')).toBeTruthy()
  })
})

describe('phone layout', () => {
  const stub = (matches: boolean) => {
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({
      matches, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent() { return false }, onchange: null,
    })) as unknown as typeof window.matchMedia
  }
  afterEach(() => { delete (window as { matchMedia?: unknown }).matchMedia })

  it('swaps the table for cards when a card renderer is given and the screen is narrow', () => {
    stub(true)
    const onRowOpen = vi.fn()
    render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} onRowOpen={onRowOpen}
      renderCard={row => <div>Card for {row.name}</div>} />)
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    fireEvent.click(screen.getByText('Card for Node 02'))
    expect(onRowOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'r2' }))
  })

  it('keeps the table on a wide screen, or without a card renderer', () => {
    stub(false)
    const { unmount } = render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} renderCard={row => <div>{row.name}</div>} />)
    expect(screen.getByRole('table')).toBeTruthy()
    unmount()
    stub(true)
    render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByRole('table')).toBeTruthy()
  })

  it('pins the first data column so it stays while the rest scrolls sideways', () => {
    stub(false)
    render(<DataTable data={mk(2)} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByRole('columnheader', { name: /Name/ }).className).toContain('sticky')
  })
})
