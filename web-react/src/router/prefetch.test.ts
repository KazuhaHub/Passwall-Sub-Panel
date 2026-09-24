import { describe, expect, it, vi } from 'vitest'
import { createViewPrefetcher } from './prefetch'

// A manual idle scheduler: each queued callback runs only when the test says the
// browser is idle, so the order and pacing of background loads are observable.
function manualIdle() {
  const pending: Array<() => void> = []
  return {
    schedule: (cb: () => void) => {
      pending.push(cb)
      return () => { const i = pending.indexOf(cb); if (i >= 0) pending.splice(i, 1) }
    },
    async runNext() {
      const cb = pending.shift()
      cb?.()
      await Promise.resolve(); await Promise.resolve(); await Promise.resolve()
    },
    get size() { return pending.length },
  }
}

describe('view prefetcher', () => {
  it('loads a view once however often it is asked, and ignores paths it does not know', async () => {
    const users = vi.fn(() => Promise.resolve({}))
    const prefetcher = createViewPrefetcher({ '/admin/users': users }, { saveData: () => false })
    await Promise.all([prefetcher.prefetch('/admin/users'), prefetcher.prefetch('/admin/users')])
    await prefetcher.prefetch('/admin/users')
    expect(users).toHaveBeenCalledTimes(1)
    expect(await prefetcher.prefetch('/admin/unknown')).toBe(false)
  })

  it('swallows a failed load and lets a later request try again', async () => {
    const users = vi.fn()
      .mockImplementationOnce(() => Promise.reject(new Error('offline')))
      .mockImplementationOnce(() => Promise.resolve({}))
    const prefetcher = createViewPrefetcher({ '/admin/users': users }, { saveData: () => false })
    expect(await prefetcher.prefetch('/admin/users')).toBe(false)
    expect(await prefetcher.prefetch('/admin/users')).toBe(true)
    expect(users).toHaveBeenCalledTimes(2)
  })

  it('loads the idle queue one view at a time, each only when the browser is idle', async () => {
    const idle = manualIdle()
    const a = vi.fn(() => Promise.resolve({}))
    const b = vi.fn(() => Promise.resolve({}))
    const prefetcher = createViewPrefetcher({ '/a': a, '/b': b }, { scheduleIdle: idle.schedule, saveData: () => false })
    prefetcher.prefetchWhenIdle(['/a', '/b'])
    expect(a).not.toHaveBeenCalled()
    await idle.runNext()
    expect(a).toHaveBeenCalledTimes(1)
    expect(b).not.toHaveBeenCalled()
    await idle.runNext()
    expect(b).toHaveBeenCalledTimes(1)
    expect(idle.size).toBe(0)
  })

  it('stops the idle queue after a failure instead of retrying every view on a dead link', async () => {
    const idle = manualIdle()
    const a = vi.fn(() => Promise.reject(new Error('offline')))
    const b = vi.fn(() => Promise.resolve({}))
    const prefetcher = createViewPrefetcher({ '/a': a, '/b': b }, { scheduleIdle: idle.schedule, saveData: () => false })
    prefetcher.prefetchWhenIdle(['/a', '/b'])
    await idle.runNext()
    expect(a).toHaveBeenCalledTimes(1)
    expect(idle.size).toBe(0)
    expect(b).not.toHaveBeenCalled()
  })

  it('can be cancelled, and does nothing in the background when the browser asks to save data', async () => {
    const idle = manualIdle()
    const a = vi.fn(() => Promise.resolve({}))
    const prefetcher = createViewPrefetcher({ '/a': a }, { scheduleIdle: idle.schedule, saveData: () => false })
    const cancel = prefetcher.prefetchWhenIdle(['/a'])
    cancel()
    await idle.runNext()
    expect(a).not.toHaveBeenCalled()

    const saving = createViewPrefetcher({ '/a': a }, { scheduleIdle: idle.schedule, saveData: () => true })
    saving.prefetchWhenIdle(['/a'])
    expect(idle.size).toBe(0)
    // An explicit hover is still honoured: the operator is about to open the page.
    await saving.prefetch('/a')
    expect(a).toHaveBeenCalledTimes(1)
  })
})
