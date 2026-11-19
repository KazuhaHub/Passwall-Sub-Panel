// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { readTableView, tableViewKey, useTableViewState } from './useTableViewState'

beforeEach(() => localStorage.clear())
afterEach(cleanup)

const DEFAULTS = { sort: null, pageSize: 25, columnVisibility: {}, filters: {} }

describe('readTableView', () => {
  it('returns the defaults for nothing stored', () => {
    expect(readTableView(null, DEFAULTS)).toEqual(DEFAULTS)
  })

  it('survives broken JSON and wrong shapes, field by field', () => {
    expect(readTableView('{not json', DEFAULTS)).toEqual(DEFAULTS)
    expect(readTableView('42', DEFAULTS)).toEqual(DEFAULTS)
    const mixed = JSON.stringify({
      v: 1,
      sort: { id: 'name', desc: 'yes' },        // bad: desc must be boolean
      pageSize: 50,                               // good
      columns: { id: false, name: 'x', tags: true }, // only the booleans survive
      filters: { status: ['up', 3, 'down'], bad: 'up' },
    })
    expect(readTableView(mixed, DEFAULTS)).toEqual({
      sort: null,
      pageSize: 50,
      columnVisibility: { id: false, tags: true },
      filters: { status: ['up', 'down'] },
    })
  })

  it('ignores a page size the footer does not offer', () => {
    expect(readTableView(JSON.stringify({ v: 1, pageSize: 7 }), DEFAULTS).pageSize).toBe(25)
  })

  it('ignores a payload from a different version', () => {
    expect(readTableView(JSON.stringify({ v: 2, pageSize: 100 }), DEFAULTS).pageSize).toBe(25)
  })
})

describe('useTableViewState', () => {
  it('persists each change under a versioned key per page', () => {
    const { result } = renderHook(() => useTableViewState('nodes'))
    act(() => { result.current.setPageSize(50) })
    act(() => { result.current.setSort({ id: 'name', desc: true }) })
    act(() => { result.current.setColumnVisibility({ id: false }) })
    act(() => { result.current.setFilters({ status: ['up'] }) })
    const stored = JSON.parse(localStorage.getItem(tableViewKey('nodes'))!)
    expect(tableViewKey('nodes')).toContain('v1')
    expect(stored).toMatchObject({ v: 1, pageSize: 50, sort: { id: 'name', desc: true }, columns: { id: false }, filters: { status: ['up'] } })
    expect(localStorage.getItem(tableViewKey('users'))).toBeNull()
  })

  it('restores the stored view on the next visit', () => {
    localStorage.setItem(tableViewKey('nodes'), JSON.stringify({ v: 1, pageSize: 100, sort: { id: 'id', desc: false } }))
    const { result } = renderHook(() => useTableViewState('nodes'))
    expect(result.current.state.pageSize).toBe(100)
    expect(result.current.state.sort).toEqual({ id: 'id', desc: false })
  })

  it('uses the caller defaults when nothing is stored, and reset() returns to them', () => {
    const { result } = renderHook(() => useTableViewState('nodes', { pageSize: 10, sort: { id: 'name', desc: false } }))
    expect(result.current.state.pageSize).toBe(10)
    act(() => { result.current.setPageSize(100) })
    act(() => { result.current.reset() })
    expect(result.current.state.pageSize).toBe(10)
    expect(localStorage.getItem(tableViewKey('nodes'))).toBeNull()
  })

  it('does not throw when storage is unavailable', () => {
    const real = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')!
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get() { throw new Error('denied') } })
    try {
      const { result } = renderHook(() => useTableViewState('nodes'))
      act(() => { result.current.setPageSize(50) })
      expect(result.current.state.pageSize).toBe(50)
    } finally {
      Object.defineProperty(globalThis, 'localStorage', real)
    }
  })
})
