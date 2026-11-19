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
    expect(onPageChange).toHaveBeenCalledWith(3, 'user')
    fireEvent.click(screen.getByRole('button', { name: 'First page' }))
    expect(onPageChange).toHaveBeenLastCalledWith(1, 'user')
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
    // A correction says so, so a URL-backed page can replace instead of push (no Back trap).
    expect(onPageChange).toHaveBeenCalledWith(2, 'correction')
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
        bulkActions={({ ids, clear }) => (<><button onClick={() => onBulk?.(ids)}>Disable</button><button onClick={clear}>Done</button></>)}
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
  const frame = () => screen.getByRole('table').parentElement as HTMLElement
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
    frame().getBoundingClientRect = () => ({ top: 0, bottom: 160, height: 160, left: 0, right: 400, width: 400, x: 0, y: 0, toJSON() {} }) as DOMRect
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
    frame().getBoundingClientRect = () => ({ top: 0, bottom: 160, height: 160, left: 0, right: 400, width: 400, x: 0, y: 0, toJSON() {} }) as DOMRect
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

  it('turns the handles off while a column sort is on, and says why', () => {
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} sorting={[{ id: 'name', desc: false }]} pagination={false} />)
    const h = screen.getByRole('button', { name: /Drag to reorder: A/ })
    expect(h.getAttribute('aria-disabled')).toBe('true')
    // aria-disabled, not disabled: it stays focusable so its tooltip and description can be reached
    expect((h as HTMLButtonElement).disabled).toBe(false)
    const reason = document.getElementById(h.getAttribute('aria-describedby')!.split(' ').pop()!)
    expect(reason?.textContent).toContain('column sort is on')
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    expect(screen.getByRole('status').textContent).not.toContain('Picked up')
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


describe('page corrections (items 1 and 2)', () => {
  it('does not clamp while the total is not known yet: a deep link to page 3 stays on 3', () => {
    const onPageChange = vi.fn()
    render(<DataTable mode="server" data={[]} loading page={3} pageSize={25} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    expect(onPageChange).not.toHaveBeenCalled()
  })

  it('does not clamp on total 0 while loading', () => {
    const onPageChange = vi.fn()
    render(<DataTable mode="server" data={[]} total={0} loading page={3} pageSize={25} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    expect(onPageChange).not.toHaveBeenCalled()
  })

  it('corrects once the data is in and the page really is out of range', () => {
    const onPageChange = vi.fn()
    const { rerender } = render(<DataTable mode="server" data={[]} loading page={9} pageSize={25} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    rerender(<DataTable mode="server" data={mk(5)} total={30} page={9} pageSize={25} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    expect(onPageChange).toHaveBeenCalledWith(2, 'correction')
  })

  it('does not clamp a client table while it is loading either', () => {
    const onPageChange = vi.fn()
    render(<DataTable data={[]} loading page={4} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    expect(onPageChange).not.toHaveBeenCalled()
  })
})

describe('reordering needs the whole list (item 5)', () => {
  const rows = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }] as Row[]
  const cols: ColumnDef<Row>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }]

  it('is off while the list is paged, with a reason and a developer error', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    // @ts-expect-error onReorder requires pagination={false}: the type rejects this call
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} />)
    const h = screen.getByRole('button', { name: /Drag to reorder: A/ })
    expect(h.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(h.getAttribute('aria-describedby')!.split(' ').pop()!)?.textContent).toContain('only some rows')
    expect(err).toHaveBeenCalledWith(expect.stringContaining('pagination={false}'))
  })

  it('is off in server mode, where the page is only part of the list', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    // @ts-expect-error onReorder requires client mode
    render(<DataTable mode="server" data={rows} total={50} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} pagination={false} />)
    expect(screen.getByRole('button', { name: /Drag to reorder: A/ }).getAttribute('aria-disabled')).toBe('true')
  })

  it('is off in server mode even without paging, and the types say so', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    // @ts-expect-error onReorder requires client mode
    render(<DataTable mode="server" data={rows} total={50} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} pagination={false} />)
    expect(err).toHaveBeenCalledWith(expect.stringContaining('client mode'))
  })

  it('is off while the caller says so (a filter is active), with the caller\'s reason', () => {
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={() => {}} pagination={false} reorderBlockedReason="Clear the search to reorder" />)
    const h = screen.getByRole('button', { name: /Drag to reorder: A/ })
    expect(h.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(h.getAttribute('aria-describedby')!.split(' ').pop()!)?.textContent).toBe('Clear the search to reorder')
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    expect(screen.getByRole('status').textContent).not.toContain('Picked up')
  })

  it('says why a tree cannot be reordered', () => {
    const t: Row[] = [{ id: 'p', name: 'P', status: '', traffic: 0, kids: [{ id: 'c', name: 'C', status: '', traffic: 0 }] }]
    render(<DataTable data={t} columns={columns} getRowId={r => r.id} rowLabel={r => r.name} getSubRows={r => r.kids} onReorder={() => {}} pagination={false} columnMenu={false} />)
    const h = screen.getByRole('button', { name: /Drag to reorder: P/ })
    expect(h.getAttribute('aria-disabled')).toBe('true')
    expect(document.getElementById(h.getAttribute('aria-describedby')!.split(' ').pop()!)?.textContent).toContain('tree view')
  })
})

describe('row kinds, flags and hover (item 8)', () => {
  interface L { id: string; name: string; kind?: 'line' | 'separator'; landing?: string; pending?: boolean }
  const data: L[] = [
    { id: 'a', name: 'Line A', landing: 'x' },
    { id: 'sep', name: 'Premium', kind: 'separator' },
    { id: 'b', name: 'Line B', landing: 'x', pending: true },
    { id: 'c', name: 'Line C', landing: 'y' },
  ]
  const cols: ColumnDef<L>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }, { accessorKey: 'landing', header: 'Landing', enableSorting: false }]
  const base = {
    data, columns: cols, getRowId: (r: L) => r.id, rowLabel: (r: L) => r.name, pagination: false as const, columnMenu: false, selectable: true,
    rowOptions: (r: L) => (r.kind === 'separator'
      ? { fullWidth: <strong>— {r.name} —</strong>, selectable: false, openable: false }
      : { state: r.pending ? ('pending' as const) : undefined }),
  }
  const rowsOf = () => screen.getAllByRole('row').slice(1)

  it('renders a separator across the whole row in one cell, and keeps it out of selection', () => {
    render(<DataTable {...base} onReorder={() => {}} />)
    const sep = rowsOf()[1]
    const cells = within(sep).getAllByRole('cell')
    const wide = cells.find(c => c.textContent?.includes('Premium'))!
    expect(wide.getAttribute('colspan')).toBe('2')
    expect(within(sep).queryByRole('checkbox')).toBeNull()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).toContain('3 selected')
  })

  it('keeps a separator reorderable', () => {
    const onReorder = vi.fn()
    render(<DataTable {...base} onReorder={onReorder} />)
    const h = screen.getByRole('button', { name: 'Drag to reorder: Premium' })
    h.focus()
    fireEvent.keyDown(h, { key: ' ' })
    fireEvent.keyDown(h, { key: 'ArrowDown' })
    fireEvent.keyDown(h, { key: ' ' })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'sep', beforeId: 'c' })
  })

  it('does not open a row that is not openable, nor toggle it with Space', () => {
    const onRowOpen = vi.fn()
    render(<DataTable {...base} onRowOpen={onRowOpen} />)
    const sep = rowsOf()[1]
    fireEvent.click(within(sep).getByText('— Premium —'))
    sep.focus()
    fireEvent.keyDown(sep, { key: 'Enter' })
    fireEvent.keyDown(sep, { key: ' ' })
    expect(onRowOpen).not.toHaveBeenCalled()
    expect(sep.getAttribute('data-state')).toBeNull()
    fireEvent.click(within(rowsOf()[0]).getByText('Line A'))
    expect(onRowOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'a' }))
  })

  it('marks a pending row and applies a per-row class and attributes', () => {
    render(<DataTable {...base} rowOptions={r => ({ state: r.pending ? 'pending' : r.id === 'c' ? 'dimmed' : undefined, className: r.id === 'a' ? 'my-class' : undefined, data: r.id === 'a' ? { 'data-kind': 'line' } : undefined })} />)
    expect(rowsOf()[2].getAttribute('data-row-state')).toBe('pending')
    expect(rowsOf()[3].getAttribute('data-row-state')).toBe('dimmed')
    expect(rowsOf()[0].className).toContain('my-class')
    expect(rowsOf()[0].getAttribute('data-kind')).toBe('line')
  })

  it('reports hover and keyboard focus by row id, and null when it leaves', () => {
    const onRowHover = vi.fn()
    render(<DataTable {...base} onRowHover={onRowHover} onRowOpen={() => {}} />)
    fireEvent.mouseEnter(rowsOf()[0])
    expect(onRowHover).toHaveBeenLastCalledWith('a')
    fireEvent.mouseLeave(rowsOf()[0])
    expect(onRowHover).toHaveBeenLastCalledWith(null)
    rowsOf()[2].focus()
    expect(onRowHover).toHaveBeenLastCalledWith('b')
    fireEvent.blur(rowsOf()[2])
    expect(onRowHover).toHaveBeenLastCalledWith(null)
  })

  it('marks rows highlighted from outside', () => {
    render(<DataTable {...base} highlightedIds={['a', 'b']} />)
    expect(rowsOf().map(r => r.getAttribute('data-highlight'))).toEqual(['true', null, 'true', null])
  })
})

describe('sorting (item 9)', () => {
  const sortCol: ColumnDef<{ id: string; name: string | null; n: number | null }>[] = [
    { accessorKey: 'name', header: 'Name' }, { accessorKey: 'n', header: 'N' },
  ]
  const mount = (rows: { id: string; name: string | null; n: number | null }[]) =>
    render(<DataTable data={rows} columns={sortCol} getRowId={r => r.id} pagination={false} columnMenu={false} />)
  const click = (name: string) => fireEvent.click(within(screen.getByRole('columnheader', { name })).getByRole('button'))
  const col0 = () => screen.getAllByRole('row').slice(1).map(r => within(r).getAllByRole('cell')[0].textContent)

  it('compares numbers inside text numerically', () => {
    mount([{ id: '1', name: 'item10', n: 1 }, { id: '2', name: 'item9', n: 2 }, { id: '3', name: 'item2', n: 3 }])
    click('Name')
    expect(col0()).toEqual(['item2', 'item9', 'item10'])
  })

  it('compares numbers numerically, not as text', () => {
    mount([{ id: '1', name: 'a', n: 100 }, { id: '2', name: 'b', n: 9 }])
    click('N')
    expect(col0()).toEqual(['b', 'a'])
  })

  it('puts empty values last in both directions', () => {
    mount([{ id: '1', name: null, n: null }, { id: '2', name: 'b', n: 2 }, { id: '3', name: '', n: 1 }, { id: '4', name: 'a', n: 3 }])
    click('Name')
    expect(col0()).toEqual(['a', 'b', '', ''])
    click('Name')
    expect(col0()).toEqual(['b', 'a', '', ''])
  })

  it('sorts Chinese by pinyin when the UI language is Chinese', async () => {
    const i18n = (await import('@/test/initI18n')).default
    await i18n.changeLanguage('zh-CN')
    try {
      mount([{ id: '1', name: '张三', n: 1 }, { id: '2', name: '阿里', n: 2 }, { id: '3', name: '李四', n: 3 }])
      click('Name')
      expect(col0()).toEqual(['阿里', '李四', '张三'])
    } finally {
      await i18n.changeLanguage('en-US')
    }
  })
})

describe('selection across pages and filters (item 10)', () => {
  function Harness({ rows, onCtx }: { rows: Row[]; onCtx?: (c: { ids: string[]; hiddenCount: number; loadedRows: Row[] }) => void }) {
    const [sel, setSel] = useState<Record<string, boolean>>({ r1: true, r2: true, r30: true })
    return (
      <DataTable
        data={rows} columns={columns} getRowId={r => r.id} selectable selection={sel} onSelectionChange={setSel}
        pageSize={10} rowLabel={r => r.name}
        bulkActions={ctx => { onCtx?.(ctx); return <button>Act</button> }}
      />
    )
  }

  it('counts every selected id, and says how many are not on this page, with a way to drop them', () => {
    const seen = vi.fn()
    render(<Harness rows={mk(40)} onCtx={seen} />)
    const bar = screen.getByRole('region', { name: 'Bulk actions' })
    expect(bar.textContent).toContain('3 selected')
    expect(bar.textContent).toContain('1 not in the current list')
    expect(seen).toHaveBeenLastCalledWith(expect.objectContaining({ ids: ['r1', 'r2', 'r30'], hiddenCount: 1 }))
    // client mode holds every row, so the off-page one is still resolved
    expect(seen.mock.lastCall![0].loadedRows.map((r: Row) => r.id)).toEqual(['r1', 'r2', 'r30'])
    fireEvent.click(within(bar).getByRole('button', { name: 'Deselect items not in the current list' }))
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).toContain('2 selected')
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).not.toContain('not in the current list')
  })

  it('treats a row filtered out of the data as not on the page', () => {
    render(<Harness rows={mk(40).filter(r => r.id !== 'r2')} />)
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).toContain('2 not in the current list')
  })

  it('shows the header checkbox as indeterminate when only some of the page is selected', () => {
    render(<Harness rows={mk(40)} />)
    const all = screen.getByRole('checkbox', { name: 'Select all rows on this page' })
    expect(all.getAttribute('aria-checked')).toBe('mixed')
  })
})

describe('keyboard and focus details (items 11, 12)', () => {
  it('keeps the focused row out from under the sticky header', () => {
    render(<DataTable data={mk(3)} columns={columns} getRowId={r => r.id} />)
    expect(screen.getByRole('table').parentElement!.className).toContain('scroll-pt-')
  })

  it('keeps focus inside the pagination after First or Last is used', () => {
    render(<DataTable data={mk(60)} columns={columns} getRowId={r => r.id} pageSize={10} />)
    fireEvent.click(screen.getByRole('button', { name: 'Last page' }))
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Previous page' }))
    fireEvent.click(screen.getByRole('button', { name: 'First page' }))
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Next page' }))
  })

  it('a button that cannot act is aria-disabled and does nothing', () => {
    const onPageChange = vi.fn()
    render(<DataTable data={mk(5)} columns={columns} getRowId={r => r.id} onPageChange={onPageChange} />)
    const first = screen.getByRole('button', { name: 'First page' })
    expect(first.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(first)
    expect(onPageChange).not.toHaveBeenCalled()
  })
})

describe('pointer drag details (item 13)', () => {
  const rows = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }, { id: 'c', name: 'C' }] as Row[]
  const cols: ColumnDef<Row>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }]
  const setup = () => {
    const onReorder = vi.fn()
    render(<DataTable data={rows} columns={cols} getRowId={r => r.id} rowLabel={r => r.name} onReorder={onReorder} pagination={false} columnMenu={false} />)
    const frame = screen.getByRole('table').parentElement as HTMLElement
    frame.getBoundingClientRect = () => ({ top: 100, bottom: 300, height: 200, left: 0, right: 400, width: 400, x: 0, y: 100, toJSON() {} }) as DOMRect
    screen.getAllByRole('row').slice(1).forEach((tr, i) => {
      tr.getBoundingClientRect = () => ({ top: 100 + i * 40 - frame.scrollTop, bottom: 140 + i * 40 - frame.scrollTop, height: 40, left: 0, right: 400, width: 400, x: 0, y: 0, toJSON() {} }) as DOMRect
    })
    return { onReorder, frame, handle: screen.getByRole('button', { name: 'Drag to reorder: A' }) }
  }

  it('keeps scrolling while the pointer is held still at the edge', () => {
    vi.useFakeTimers({ toFake: ['requestAnimationFrame', 'cancelAnimationFrame'] })
    try {
      const { frame, handle } = setup()
      act(() => { handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 110, clientX: 10 })) })
      act(() => { window.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientY: 295, clientX: 10 })) })
      act(() => { vi.advanceTimersByTime(20) })
      const early = frame.scrollTop
      act(() => { vi.advanceTimersByTime(200) })
      expect(frame.scrollTop).toBeGreaterThan(early)
      act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })) })
    } finally {
      vi.useRealTimers()
    }
  })

  it('cancels when released far outside the table', () => {
    const { onReorder, handle } = setup()
    act(() => { handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 110, clientX: 10 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientY: 250, clientX: 10 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientY: 250, clientX: 900 })) })
    expect(onReorder).not.toHaveBeenCalled()
  })

  it('still commits a release just outside the edge', () => {
    const { onReorder, handle } = setup()
    act(() => { handle.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientY: 110, clientX: 10 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientY: 290, clientX: 10 })) })
    act(() => { window.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, clientY: 290, clientX: 10 })) })
    expect(onReorder).toHaveBeenCalledWith({ movedId: 'a', beforeId: null })
  })
})

describe('phone cards (item 14)', () => {
  const stub = () => {
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({
      matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent() { return false }, onchange: null,
    })) as unknown as typeof window.matchMedia
  }
  afterEach(() => { delete (window as { matchMedia?: unknown }).matchMedia })

  it('keeps the table label, offers select all, and makes cards focusable and operable', () => {
    stub()
    const onRowOpen = vi.fn()
    function H() {
      const [sel, setSel] = useState<Record<string, boolean>>({})
      return <DataTable aria-label="Nodes" data={mk(3)} columns={columns} getRowId={r => r.id} selectable selection={sel} onSelectionChange={setSel} rowLabel={r => r.name} onRowOpen={onRowOpen} renderCard={r => <span>{r.name}</span>} />
    }
    render(<H />)
    expect(screen.getByRole('list', { name: 'Nodes' })).toBeTruthy()
    const cards = screen.getAllByRole('listitem').filter(li => li.hasAttribute('data-row'))
    expect(cards).toHaveLength(3)
    expect(cards.every(c => c.tabIndex >= -1)).toBe(true)
    cards[0].focus()
    fireEvent.keyDown(cards[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(cards[1])
    fireEvent.keyDown(cards[1], { key: ' ' })
    expect(cards[1].getAttribute('data-state')).toBe('selected')
    fireEvent.keyDown(cards[1], { key: 'Enter' })
    expect(onRowOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'r2' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    expect(screen.getByRole('region', { name: 'Bulk actions' }).textContent).toContain('3 selected')
  })
})

describe('touch targets (item 15) and header names (item 26)', () => {
  it('gives sort buttons and icon buttons the coarse-pointer hit area', () => {
    render(<DataTable data={mk(30)} columns={columns} getRowId={r => r.id} pageSize={10} />)
    expect(within(screen.getByRole('columnheader', { name: /Name/ })).getByRole('button').className).toContain('pointer-coarse:min-h-')
    expect(screen.getByRole('button', { name: 'Next page' }).className).toContain('pointer-coarse:min-h-')
  })

  it('never leaves a column header empty: axe flags it', () => {
    const cols: ColumnDef<Row>[] = [...columns, { id: 'act', header: '', cell: () => 'x', enableSorting: false }]
    render(<DataTable data={mk(2)} columns={cols} getRowId={r => r.id} onReorder={() => {}} pagination={false} />)
    for (const th of screen.getAllByRole('columnheader')) expect(th.textContent!.trim().length).toBeGreaterThan(0)
    expect(screen.getByRole('columnheader', { name: 'Actions' })).toBeTruthy()
    expect(screen.getByRole('columnheader', { name: 'Reorder' })).toBeTruthy()
  })
})

describe('dimmed rows keep their text readable (item 19)', () => {
  it('softens with colour, never with opacity (which takes muted text to about 2:1)', () => {
    render(<DataTable data={[{ id: 'a', name: 'A', status: '', traffic: 0 }]} columns={columns} getRowId={r => r.id} rowOptions={() => ({ state: 'dimmed' })} pagination={false} columnMenu={false} />)
    const row = screen.getAllByRole('row')[1]
    expect(row.className).toContain('text-foreground/70')
    expect(row.className).not.toMatch(/opacity-/)
  })
})

describe('separator rows are never stale (item 2)', () => {
  interface L { id: string; name: string; kind?: 'line' | 'separator' }
  const data: L[] = [{ id: 'a', name: 'Line A' }, { id: 'sep', name: 'Premium', kind: 'separator' }, { id: 'b', name: 'Line B' }]
  const cols: ColumnDef<L>[] = [{ accessorKey: 'name', header: 'Name', enableSorting: false }]

  function Page({ count, busy, canEdit }: { count: number; busy: boolean; canEdit: boolean }) {
    return (
      <DataTable
        data={data} columns={cols} getRowId={r => r.id} pagination={false} columnMenu={false}
        rowOptions={r => (r.kind === 'separator'
          ? { fullWidth: <span>{r.name}: {count} lines{busy ? ' (busy)' : ''}{canEdit ? <button>Edit separator</button> : null}</span>, selectable: false, openable: false }
          : undefined)}
      />
    )
  }

  it('shows content that comes from outer state, not only from the row', () => {
    const { rerender } = render(<Page count={2} busy={false} canEdit={false} />)
    expect(screen.getByText(/Premium: 2 lines/)).toBeTruthy()
    rerender(<Page count={5} busy={false} canEdit={false} />)
    expect(screen.getByText(/Premium: 5 lines/)).toBeTruthy()
  })

  it('follows a busy flag and a permission change', () => {
    const { rerender } = render(<Page count={2} busy={false} canEdit={false} />)
    expect(screen.queryByText(/\(busy\)/)).toBeNull()
    rerender(<Page count={2} busy canEdit={false} />)
    expect(screen.getByText(/\(busy\)/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Edit separator' })).toBeNull()
    rerender(<Page count={2} busy canEdit />)
    expect(screen.getByRole('button', { name: 'Edit separator' })).toBeTruthy()
    rerender(<Page count={2} busy canEdit={false} />)
    expect(screen.queryByRole('button', { name: 'Edit separator' })).toBeNull()
  })
})

describe('handlers that arrive after the first render (item 2)', () => {
  const data = mk(3)
  it('onRowHover and onRowOpen defined later still reach rows that were already rendered', () => {
    const onHover = vi.fn()
    const onOpen = vi.fn()
    const { rerender } = render(<DataTable data={data} columns={columns} getRowId={r => r.id} />)
    rerender(<DataTable data={data} columns={columns} getRowId={r => r.id} onRowHover={onHover} onRowOpen={onOpen} />)
    const row = screen.getAllByRole('row')[1]
    fireEvent.mouseEnter(row)
    expect(onHover).toHaveBeenLastCalledWith('r1')
    fireEvent.click(within(row).getByText('Node 01'))
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'r1' }))
  })
})
