// @vitest-environment jsdom
import { act, cleanup, render } from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BASENAMES, renderHookInRouter } from '@/test/routerHarness'
import { urlCodecs, useUrlState, useUrlStates } from './useUrlState'
import { useUrlSelection } from './useUrlSelection'

afterEach(cleanup)

function Plain() {
  useUrlState('q', urlCodecs.string())
  return null
}

describe('urlCodecs', () => {
  it('int falls back to the default for anything that is not a plain integer in range', () => {
    const c = urlCodecs.int(1, { min: 1, max: 50 })
    expect(c.parse('7')).toBe(7)
    expect(c.parse('7.5')).toBe(1)
    expect(c.parse('abc')).toBe(1)
    expect(c.parse('0')).toBe(1)
    expect(c.parse(null)).toBe(1)
    expect(c.serialize(1)).toBeNull()
    expect(c.serialize(3)).toBe('3')
  })

  it('oneOf ignores values outside the set, so a hand-edited link cannot invent a tab', () => {
    const c = urlCodecs.oneOf(['queue', 'live', 'rules'] as const, 'queue')
    expect(c.parse('live')).toBe('live')
    expect(c.parse('nope')).toBe('queue')
    expect(c.serialize('queue')).toBeNull()
  })

  it('list round-trips and drops empties', () => {
    const c = urlCodecs.list()
    expect(c.parse('a,b,,c')).toEqual(['a', 'b', 'c'])
    expect(c.parse(null)).toEqual([])
    expect(c.serialize(['a', 'b'])).toBe('a,b')
    expect(c.serialize([])).toBeNull()
  })

  it('bool is present-or-absent', () => {
    const c = urlCodecs.bool(false)
    expect(c.parse('1')).toBe(true)
    expect(c.parse(null)).toBe(false)
    expect(c.serialize(true)).toBe('1')
    expect(c.serialize(false)).toBeNull()
  })
})

describe.each(BASENAMES)('under basename %j', base => {
const mount = <T,>(hook: () => T, entries?: string[]) => renderHookInRouter(hook, entries, base)
describe('useUrlState', () => {
  it('reads the value from the query string, default when absent', () => {
    const { result } = mount(() => useUrlState('tab', urlCodecs.oneOf(['a', 'b'] as const, 'a')), ['/?tab=b'])
    expect(result.current[0]).toBe('b')
  })

  it('writes with replace by default: Back does not walk through every filter change', async () => {
    const { result, router, where } = mount(() => useUrlState('q', urlCodecs.string()), ['/list'])
    await act(async () => { result.current[1]('abc') })
    expect(where().search).toBe('?q=abc')
    // the basename must not be applied twice (/panel/panel/list is a route that does not exist)
    expect(where().pathname).toBe('/list')
    expect(router.state.historyAction).toBe('REPLACE')
  })

  it('pushes when asked, for state that Back should undo', async () => {
    const { result, router, where } = mount(() => useUrlState('q', urlCodecs.string(), { history: 'push' }), ['/list'])
    await act(async () => { result.current[1]('abc') })
    expect(router.state.historyAction).toBe('PUSH')
    await act(async () => { await router.navigate(-1) })
    expect(where().search).toBe('')
  })

  it('lets one call override the hook default', async () => {
    const { result, router } = mount(() => useUrlState('q', urlCodecs.string()), ['/'])
    await act(async () => { result.current[1]('x', { history: 'push' }) })
    expect(router.state.historyAction).toBe('PUSH')
  })

  it('removes the param when the value equals the default and keeps other params', async () => {
    const { result, where } = mount(() => useUrlState('page', urlCodecs.int(1, { min: 1 })), ['/?page=3&q=hi'])
    await act(async () => { result.current[1](1) })
    expect(where().search).toBe('?q=hi')
  })

  it('accepts an updater function', async () => {
    const { result } = mount(() => useUrlState('page', urlCodecs.int(1, { min: 1 })), ['/?page=2'])
    await act(async () => { result.current[1](p => p + 1) })
    expect(result.current[0]).toBe(3)
  })
})

describe('useUrlStates', () => {
  const schema = {
    tab: urlCodecs.oneOf(['queue', 'live'] as const, 'queue'),
    level: urlCodecs.list(),
    page: urlCodecs.int(1, { min: 1 }),
  }

  it('patches several keys in one navigation', async () => {
    const { result, where } = mount(() => useUrlStates(schema), ['/risk'])
    await act(async () => { result.current[1]({ tab: 'live', level: ['high', 'low'], page: 1 }) })
    expect(where().search).toBe('?tab=live&level=high%2Clow')
    expect(result.current[0]).toEqual({ tab: 'live', level: ['high', 'low'], page: 1 })
  })

  it('composes two patches made in the same tick instead of the last one winning', async () => {
    const { result } = mount(() => useUrlStates(schema), ['/risk'])
    await act(async () => {
      result.current[1]({ tab: 'live' })
      result.current[1]({ page: 4 })
    })
    expect(result.current[0]).toEqual({ tab: 'live', level: [], page: 4 })
  })
})

describe('writes from several hooks in the same tick', () => {
  const q = urlCodecs.string()
  const page = urlCodecs.int(1, { min: 1 })

  it('compose instead of the last hook overwriting the others', async () => {
    const { result, where } = mount(() => ({
      a: useUrlState('q', q),
      b: useUrlState('page', page),
    }), ['/list?page=3'])
    await act(async () => {
      result.current.a[1]('hi')
      result.current.b[1](1)
    })
    expect(where().search).toBe('?q=hi')
  })

  it('keep every key when a selection and a filter change together', async () => {
    const { result, router } = mount(() => ({
      f: useUrlStates({ q }),
      sel: useUrlSelection('row'),
    }), ['/nodes'])
    await act(async () => {
      result.current.f[1]({ q: 'hk' })
      result.current.sel.select('n1')
    })
    expect(new URLSearchParams(router.state.location.search).get('q')).toBe('hk')
    expect(new URLSearchParams(router.state.location.search).get('row')).toBe('n1')
  })

  it('make one history entry, and a push anywhere in the batch wins over replace', async () => {
    const { result, router, where } = mount(() => ({
      a: useUrlState('q', q),
      b: useUrlState('tab', q, { history: 'push' }),
    }), ['/list'])
    await act(async () => {
      result.current.a[1]('x')
      result.current.b[1]('y')
    })
    expect(router.state.historyAction).toBe('PUSH')
    expect(where().search).toBe('?q=x&tab=y')
    await act(async () => { await router.navigate(-1) })
    expect(where().search).toBe('')
  })

  it('and a later tick builds on the earlier write, not on a stale location', async () => {
    const { result, where } = mount(() => ({ a: useUrlState('q', q), b: useUrlState('page', page) }), ['/list'])
    await act(async () => { result.current.a[1]('x') })
    await act(async () => { result.current.b[1](2) })
    expect(where().search).toBe('?q=x&page=2')
  })
})


describe('a batch belongs to the route it was written on (item 2)', () => {
  const q = urlCodecs.string()
  const useWithNav = () => ({ a: useUrlState('q', q), nav: useNavigate() })

  it('write then navigate: the old page\'s keys do not follow onto the new route', async () => {
    const { result, where } = mount(useWithNav, ['/list'])
    await act(async () => {
      result.current.a[1]('x')
      void result.current.nav('/other?z=1')
    })
    expect(where()).toEqual({ pathname: '/other', search: '?z=1' })
  })

  it('navigate then write: the write was made on the old page, so it is dropped too', async () => {
    const { result, where } = mount(useWithNav, ['/list'])
    await act(async () => {
      void result.current.nav('/other?z=1')
      result.current.a[1]('x')
    })
    expect(where()).toEqual({ pathname: '/other', search: '?z=1' })
  })

  it('writes made while staying on the page still land', async () => {
    const { result, where } = mount(useWithNav, ['/list?z=1'])
    await act(async () => { result.current.a[1]('x') })
    expect(where()).toEqual({ pathname: '/list', search: '?z=1&q=x' })
  })
})

describe('without a data router (item 7)', () => {
  it('says so in development instead of silently losing writes', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<MemoryRouter><Plain /></MemoryRouter>)).toThrow(/data router/i)
    err.mockRestore()
  })
})

})
