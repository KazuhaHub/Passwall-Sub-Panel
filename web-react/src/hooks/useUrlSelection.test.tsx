// @vitest-environment jsdom
import { act, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { BASENAMES, renderHookInRouter } from '@/test/routerHarness'
import { urlCodecs, useUrlStates } from './useUrlState'
import { useUrlSelection } from './useUrlSelection'

afterEach(cleanup)

describe.each(BASENAMES)('useUrlSelection under basename %j', base => {
  const mount = <T,>(hook: () => T, entries?: string[]) => renderHookInRouter(hook, entries, base)
  const q = urlCodecs.string()

  it('reads the selected id from the URL, null when absent', () => {
    const a = mount(() => useUrlSelection('row'), ['/nodes?row=12'])
    expect(a.result.current.selected).toBe('12')
    cleanup()
    const b = mount(() => useUrlSelection('row'), ['/nodes'])
    expect(b.result.current.selected).toBeNull()
    expect(b.result.current.isOpen).toBe(false)
  })

  it('opening a record pushes an entry, so Back closes it', async () => {
    const { result, router, where } = mount(() => useUrlSelection('row'), ['/nodes?status=down'])
    await act(async () => { result.current.select('7') })
    expect(where()).toEqual({ pathname: '/nodes', search: '?status=down&row=7' })
    expect(router.state.historyAction).toBe('PUSH')
    await act(async () => { await router.navigate(-1) })
    expect(where().search).toBe('?status=down')
    expect(result.current.selected).toBeNull()
  })

  it('moving to another row while open replaces, so Back still leaves with one step', async () => {
    const { result, router } = mount(() => useUrlSelection('row'), ['/nodes'])
    await act(async () => { result.current.select('7') })
    await act(async () => { result.current.select('8') })
    expect(router.state.historyAction).toBe('REPLACE')
    expect(result.current.selected).toBe('8')
    await act(async () => { await router.navigate(-1) })
    expect(result.current.selected).toBeNull()
  })

  it('parses and formats typed ids', async () => {
    const { result, where } = mount(
      () => useUrlSelection<number>('user', { parse: raw => (raw && /^\d+$/.test(raw) ? Number(raw) : null) }),
      ['/users?user=abc'],
    )
    expect(result.current.selected).toBeNull()
    await act(async () => { result.current.select(41) })
    expect(where().search).toBe('?user=41')
    expect(result.current.selected).toBe(41)
  })

  describe('closing from the UI', () => {
    it('removes the selection and keeps every other current param, on the same page', async () => {
      const { result, where } = mount(() => ({ sel: useUrlSelection('row'), f: useUrlStates({ q }) }), ['/nodes?q=old'])
      await act(async () => { result.current.sel.select('7') })
      await act(async () => { result.current.f[1]({ q: 'new' }) })
      expect(where().search).toContain('q=new')
      await act(async () => { result.current.sel.clear() })
      expect(where()).toEqual({ pathname: '/nodes', search: '?q=new' })
    })

    it('on a deep link replaces the param away and stays', async () => {
      const { result, router, where } = mount(() => useUrlSelection('row'), ['/nodes?row=7&q=hk'])
      await act(async () => { result.current.clear() })
      expect(where().search).toBe('?q=hk')
      expect(router.state.historyAction).toBe('REPLACE')
    })

    it('does not reopen the panel: Back after closing lands on the list as it was before opening', async () => {
      const { result, router, where } = mount(() => useUrlSelection('row'), ['/nodes'])
      await act(async () => { result.current.select('7') })
      await act(async () => { result.current.clear() })
      expect(where().search).toBe('')
      await act(async () => { await router.navigate(-1) })
      // Documented trade-off of the simple scheme: closing replaces, so the entry
      // from before the panel opened is still behind us. One extra Back, never a reopen.
      expect(where()).toEqual({ pathname: '/nodes', search: '' })
      expect(result.current.selected).toBeNull()
    })

    it('composes with another write in the same tick, in either order', async () => {
      const m = mount(() => ({ sel: useUrlSelection('row'), f: useUrlStates({ q }) }), ['/list?row=3'])
      await act(async () => {
        m.result.current.sel.clear()
        m.result.current.f[1]({ q: 'kw' })
      })
      expect(m.where().search).toBe('?q=kw')
      cleanup()
      const n = mount(() => ({ sel: useUrlSelection('row'), f: useUrlStates({ q }) }), ['/list?row=3'])
      await act(async () => {
        n.result.current.f[1]({ q: 'kw' })
        n.result.current.sel.clear()
      })
      expect(n.where().search).toBe('?q=kw')
    })
  })
})
