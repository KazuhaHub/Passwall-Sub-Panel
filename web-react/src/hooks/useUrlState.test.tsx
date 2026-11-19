// @vitest-environment jsdom
import { act, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { renderHookInRouter } from '@/test/routerHarness'
import { urlCodecs, useUrlState, useUrlStates } from './useUrlState'

afterEach(cleanup)

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

describe('useUrlState', () => {
  it('reads the value from the query string, default when absent', () => {
    const { result } = renderHookInRouter(() => useUrlState('tab', urlCodecs.oneOf(['a', 'b'] as const, 'a')), ['/?tab=b'])
    expect(result.current[0]).toBe('b')
  })

  it('writes with replace by default: Back does not walk through every filter change', async () => {
    const { result, router } = renderHookInRouter(() => useUrlState('q', urlCodecs.string()), ['/list'])
    await act(async () => { result.current[1]('abc') })
    expect(router.state.location.search).toBe('?q=abc')
    expect(router.state.historyAction).toBe('REPLACE')
  })

  it('pushes when asked, for state that Back should undo', async () => {
    const { result, router } = renderHookInRouter(() => useUrlState('q', urlCodecs.string(), { history: 'push' }), ['/list'])
    await act(async () => { result.current[1]('abc') })
    expect(router.state.historyAction).toBe('PUSH')
    await act(async () => { await router.navigate(-1) })
    expect(router.state.location.search).toBe('')
  })

  it('lets one call override the hook default', async () => {
    const { result, router } = renderHookInRouter(() => useUrlState('q', urlCodecs.string()), ['/'])
    await act(async () => { result.current[1]('x', { history: 'push' }) })
    expect(router.state.historyAction).toBe('PUSH')
  })

  it('removes the param when the value equals the default and keeps other params', async () => {
    const { result, router } = renderHookInRouter(() => useUrlState('page', urlCodecs.int(1, { min: 1 })), ['/?page=3&q=hi'])
    await act(async () => { result.current[1](1) })
    expect(router.state.location.search).toBe('?q=hi')
  })

  it('accepts an updater function', async () => {
    const { result } = renderHookInRouter(() => useUrlState('page', urlCodecs.int(1, { min: 1 })), ['/?page=2'])
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
    const { result, router } = renderHookInRouter(() => useUrlStates(schema), ['/risk'])
    await act(async () => { result.current[1]({ tab: 'live', level: ['high', 'low'], page: 1 }) })
    expect(router.state.location.search).toBe('?tab=live&level=high%2Clow')
    expect(result.current[0]).toEqual({ tab: 'live', level: ['high', 'low'], page: 1 })
  })

  it('composes two patches made in the same tick instead of the last one winning', async () => {
    const { result } = renderHookInRouter(() => useUrlStates(schema), ['/risk'])
    await act(async () => {
      result.current[1]({ tab: 'live' })
      result.current[1]({ page: 4 })
    })
    expect(result.current[0]).toEqual({ tab: 'live', level: [], page: 4 })
  })
})
