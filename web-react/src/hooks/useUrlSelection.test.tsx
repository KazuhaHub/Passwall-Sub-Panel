// @vitest-environment jsdom
import { act, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { renderHookInRouter } from '@/test/routerHarness'
import { useUrlSelection } from './useUrlSelection'

afterEach(cleanup)

describe('useUrlSelection', () => {
  it('reads the selected id from the URL, null when absent', () => {
    const a = renderHookInRouter(() => useUrlSelection('row'), ['/nodes?row=12'])
    expect(a.result.current.selected).toBe('12')
    cleanup()
    const b = renderHookInRouter(() => useUrlSelection('row'), ['/nodes'])
    expect(b.result.current.selected).toBeNull()
    expect(b.result.current.isOpen).toBe(false)
  })

  it('opening a record pushes an entry, so Back closes it', async () => {
    const { result, router } = renderHookInRouter(() => useUrlSelection('row'), ['/nodes?status=down'])
    await act(async () => { result.current.select('7') })
    expect(router.state.location.search).toBe('?status=down&row=7')
    expect(router.state.historyAction).toBe('PUSH')
    await act(async () => { await router.navigate(-1) })
    expect(router.state.location.search).toBe('?status=down')
    expect(result.current.selected).toBeNull()
  })

  it('moving to another row while open replaces, so Back still leaves with one step', async () => {
    const { result, router } = renderHookInRouter(() => useUrlSelection('row'), ['/nodes'])
    await act(async () => { result.current.select('7') })
    await act(async () => { result.current.select('8') })
    expect(router.state.historyAction).toBe('REPLACE')
    expect(result.current.selected).toBe('8')
    await act(async () => { await router.navigate(-1) })
    expect(result.current.selected).toBeNull()
  })

  it('close() after we opened it goes Back, leaving no dead entry behind', async () => {
    const { result, router } = renderHookInRouter(() => useUrlSelection('row'), ['/nodes'])
    await act(async () => { result.current.select('7') })
    await act(async () => { result.current.clear() })
    expect(router.state.location.search).toBe('')
    expect(router.state.historyAction).toBe('POP')
  })

  it('close() on a deep link replaces the param away and stays on the page', async () => {
    const { result, router } = renderHookInRouter(() => useUrlSelection('row'), ['/nodes?row=7&q=hk'])
    await act(async () => { result.current.clear() })
    expect(router.state.location.search).toBe('?q=hk')
    expect(router.state.historyAction).toBe('REPLACE')
  })

  it('parses and formats typed ids', async () => {
    const { result, router } = renderHookInRouter(
      () => useUrlSelection<number>('user', { parse: raw => (raw && /^\d+$/.test(raw) ? Number(raw) : null) }),
      ['/users?user=abc'],
    )
    expect(result.current.selected).toBeNull()
    await act(async () => { result.current.select(41) })
    expect(router.state.location.search).toBe('?user=41')
    expect(result.current.selected).toBe(41)
  })
})
