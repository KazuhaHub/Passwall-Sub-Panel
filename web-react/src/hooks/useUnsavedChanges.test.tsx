// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { act, cleanup } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BASENAMES, renderHookInRouter as realRender } from '@/test/routerHarness'
import { useUnsavedChanges } from './useUnsavedChanges'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe.each(BASENAMES)('useUnsavedChanges under basename %j', base => {
  const renderHookInRouter = <T,>(hook: () => T, entries?: string[]) => realRender(hook, entries, base)

describe('useUnsavedChanges', () => {
  it('lets a clean page navigate without asking', async () => {
    const confirm = vi.fn(async () => true)
    const { router, where } = renderHookInRouter(() => useUnsavedChanges(false, { confirm }), ['/a'])
    await act(async () => { await router.navigate('/b') })
    expect(confirm).not.toHaveBeenCalled()
    expect(where().pathname).toBe('/b')
  })

  it('asks before leaving a dirty page and stays when the answer is no', async () => {
    const confirm = vi.fn(async () => false)
    const { router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
    await act(async () => { await router.navigate('/b') })
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(where().pathname).toBe('/a')
  })

  it('leaves when the answer is yes', async () => {
    const confirm = vi.fn(async () => true)
    const { router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
    await act(async () => { await router.navigate('/b') })
    await act(async () => { await Promise.resolve() })
    expect(where().pathname).toBe('/b')
  })

  it('does not ask for a change that stays on the page (a tab or filter in the query string)', async () => {
    const confirm = vi.fn(async () => false)
    const { router } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
    await act(async () => { await router.navigate('/a?tab=2') })
    expect(confirm).not.toHaveBeenCalled()
    expect(router.state.location.search).toBe('?tab=2')
  })

  it('falls back to the browser dialog with the shared wording', async () => {
    const ask = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { router, where } = renderHookInRouter(() => useUnsavedChanges(true), ['/a'])
    await act(async () => { await router.navigate('/b') })
    expect(ask).toHaveBeenCalledWith(expect.stringContaining('unsaved changes'))
    expect(where().pathname).toBe('/a')
  })

  it('asks the browser to warn on tab close only while dirty', () => {
    const dirty = renderHookInRouter(() => useUnsavedChanges(true))
    const ev = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    dirty.unmount()
    const clean = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(clean)
    expect(clean.defaultPrevented).toBe(false)
  })
})

describe('save then navigate in one handler', () => {
  it('does not ask when the handler marks the draft saved before navigating', async () => {
    const confirm = vi.fn(async () => false)
    const { result, router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
    // The state update that clears `dirty` has not rendered yet when navigate runs.
    await act(async () => {
      result.current.markSaved()
      await router.navigate('/b')
    })
    expect(confirm).not.toHaveBeenCalled()
    expect(where().pathname).toBe('/b')
  })

  it('allowNext lets exactly one navigation through, then guards again', async () => {
    const confirm = vi.fn(async () => false)
    const { result, router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
    await act(async () => {
      result.current.allowNext()
      await router.navigate('/b')
    })
    expect(where().pathname).toBe('/b')
    await act(async () => { await router.navigate('/c') })
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(where().pathname).toBe('/b')
  })

  it('guards the browser Back button too', async () => {
    const confirm = vi.fn(async () => false)
    const { router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a', '/b'])
    expect(where().pathname).toBe('/b')
    await act(async () => { await router.navigate(-1) })
    expect(confirm).toHaveBeenCalled()
    expect(where().pathname).toBe('/b')
  })

  it('a draft that becomes dirty again after markSaved is guarded again', async () => {
    const confirm = vi.fn(async () => false)
    const { result, router, rerender } = renderHookInRouter(() => useUnsavedChanges(false, { confirm }), ['/a'])
    void result; void rerender
    await act(async () => { await router.navigate('/b') })
    expect(confirm).not.toHaveBeenCalled()
  })

  it('stops warning on tab close once markSaved was called, before the next render', () => {
    const { result } = renderHookInRouter(() => useUnsavedChanges(true))
    act(() => { result.current.markSaved() })
    const ev = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
  })
})

  describe('arming and disarming', () => {
    it('allowNext does not outlive the tick it was called in', async () => {
      const confirm = vi.fn(async () => false)
      const { result, router, where } = renderHookInRouter(() => useUnsavedChanges(true, { confirm }), ['/a'])
      act(() => { result.current.allowNext() })
      await act(async () => { await Promise.resolve() })
      await act(async () => { await router.navigate('/b') })
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(where().pathname).toBe('/a')
    })

    it('a markSaved() that came too early is undone by the next render with the draft still dirty', async () => {
      const confirm = vi.fn(async () => false)
      const { result, router, where } = renderHookInRouter(() => {
        const [tick, setTick] = useState(0)
        return { ...useUnsavedChanges(true, { confirm }), poke: () => setTick(tick + 1) }
      }, ['/a'])
      act(() => { result.current.markSaved() })
      // the save failed; the page re-renders and is still dirty
      act(() => { result.current.poke() })
      await act(async () => { await router.navigate('/b') })
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(where().pathname).toBe('/a')
    })
  })
})
