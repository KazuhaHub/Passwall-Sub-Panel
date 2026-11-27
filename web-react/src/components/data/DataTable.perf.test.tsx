// @vitest-environment jsdom
import '@/test/initI18n'
import { useMemo, useState } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { ColumnDef } from '@tanstack/react-table'
import { DataTable } from './DataTable'

afterEach(cleanup)

interface Row { id: string; name: string }
// 200 is the most an unpaginated table draws in full: above it the body is windowed (DataTable.windowed.test.tsx).
const rows: Row[] = Array.from({ length: 200 }, (_, i) => ({ id: `r${i + 1}`, name: `Line ${i + 1}` }))

describe('hovering a row (item 18)', () => {
  it('re-renders only the rows whose highlighted state changed, not all 200', () => {
    let cellRenders = 0
    function Page() {
      const [hover, setHover] = useState<string | null>(null)
      const columns = useMemo<ColumnDef<Row>[]>(() => [
        { accessorKey: 'name', header: 'Name', cell: c => { cellRenders++; return c.getValue<string>() } },
        { id: 'second', header: 'Second', cell: () => { cellRenders++; return 'x' } },
      ], [])
      return (
        <DataTable
          data={rows} columns={columns} getRowId={r => r.id} pagination={false} columnMenu={false} maxHeight={null}
          onRowOpen={() => {}} onRowHover={setHover} highlightedIds={hover ? [hover] : []}
        />
      )
    }
    render(<Page />)
    const initial = cellRenders
    expect(initial).toBeGreaterThanOrEqual(400)
    cellRenders = 0
    act(() => { fireEvent.mouseEnter(screen.getAllByRole('row')[6]) })
    const afterEnter = cellRenders
    cellRenders = 0
    act(() => { fireEvent.mouseLeave(screen.getAllByRole('row')[6]) })
    const afterLeave = cellRenders
    console.info(`hover re-render: enter ${afterEnter} cell renders, leave ${afterLeave} (initial mount ${initial})`)
    // one row, two cells, on the way in and on the way out
    expect(afterEnter).toBeLessThanOrEqual(4)
    expect(afterLeave).toBeLessThanOrEqual(4)
  })
})

describe('mounting a long list', () => {
  it('renders the cells of the window only, not of all 10 000 rows', () => {
    const big: Row[] = Array.from({ length: 10000 }, (_, i) => ({ id: `r${i + 1}`, name: `Line ${i + 1}` }))
    let cellRenders = 0
    const columns: ColumnDef<Row>[] = [
      { accessorKey: 'name', header: 'Name', cell: c => { cellRenders++; return c.getValue<string>() } },
    ]
    render(<DataTable data={big} columns={columns} getRowId={r => r.id} pagination={false} columnMenu={false} />)
    // a few dozen rows (window plus overscan), never thousands; an unwindowed table would be 10 000
    expect(cellRenders).toBeGreaterThan(0)
    expect(cellRenders).toBeLessThan(200)
    expect(screen.getAllByRole('row').length).toBeLessThan(100)
  })
})
