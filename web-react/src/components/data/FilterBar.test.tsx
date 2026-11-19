// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FilterBar } from './FilterBar'

beforeEach(() => vi.useFakeTimers())
afterEach(() => { cleanup(); vi.useRealTimers() })

const facet = { id: 'status', label: 'Status', options: [{ value: 'up', label: 'Online', count: 1 }], selected: [] as string[], onChange: () => {} }

describe('FilterBar', () => {
  it('is a search landmark with the search box first', () => {
    render(<FilterBar search={{ value: '', onChange: () => {}, placeholder: 'Search nodes' }} facets={[facet]} />)
    expect(screen.getByRole('search')).toBeTruthy()
    expect(screen.getByRole('searchbox', { name: 'Search nodes' })).toBeTruthy()
    expect(screen.getByRole('button', { name: /Status/ })).toBeTruthy()
  })

  it('waits for a pause in typing before reporting the keyword, so a server list is not asked per keystroke', () => {
    const onChange = vi.fn()
    render(<FilterBar search={{ value: '', onChange, placeholder: 'Search', debounceMs: 250 }} />)
    const box = screen.getByRole('searchbox')
    fireEvent.change(box, { target: { value: 'h' } })
    fireEvent.change(box, { target: { value: 'hk' } })
    expect(onChange).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(260) })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith('hk')
  })

  it('reports at once on Enter', () => {
    const onChange = vi.fn()
    render(<FilterBar search={{ value: '', onChange, placeholder: 'Search' }} />)
    const box = screen.getByRole('searchbox')
    fireEvent.change(box, { target: { value: 'tokyo' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(onChange).toHaveBeenCalledWith('tokyo')
  })

  it('follows a keyword changed from outside (Back, a cleared filter)', () => {
    const { rerender } = render(<FilterBar search={{ value: 'a', onChange: () => {}, placeholder: 'Search' }} />)
    rerender(<FilterBar search={{ value: '', onChange: () => {}, placeholder: 'Search' }} />)
    expect((screen.getByRole('searchbox') as HTMLInputElement).value).toBe('')
  })

  it('offers "clear all" only while something is filtered, and clears the search at once', () => {
    const onClearAll = vi.fn()
    const { rerender } = render(<FilterBar search={{ value: '', onChange: () => {}, placeholder: 'Search' }} facets={[facet]} onClearAll={onClearAll} />)
    expect(screen.queryByRole('button', { name: 'Clear all filters' })).toBeNull()
    rerender(<FilterBar search={{ value: 'hk', onChange: () => {}, placeholder: 'Search' }} facets={[{ ...facet, selected: ['up'] }]} onClearAll={onClearAll} />)
    fireEvent.click(screen.getByRole('button', { name: 'Clear all filters' }))
    expect(onClearAll).toHaveBeenCalledTimes(1)
  })

  it('clears just the search with the x inside the box', () => {
    const onChange = vi.fn()
    render(<FilterBar search={{ value: 'hk', onChange, placeholder: 'Search' }} />)
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(onChange).toHaveBeenCalledWith('')
  })
})
