// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { previewHint, useDraftPreview } from './useDraftPreview'

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

const flush = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(400) }) }

it('debounces, cancels the stale request and keeps the last answer while loading', async () => {
  const signals: AbortSignal[] = []
  const fetcher = vi.fn((signal: AbortSignal) => { signals.push(signal); return Promise.resolve(['a']) })
  const { result, rerender } = renderHook(({ key }) => useDraftPreview<string[]>(key, fetcher), { initialProps: { key: 'one' as string | null } })
  expect(result.current.status).toBe('loading')
  rerender({ key: 'two' })
  await flush()
  expect(fetcher).toHaveBeenCalledTimes(1) // the first draft never left the debounce
  expect(result.current).toEqual({ status: 'ready', data: ['a'] })
  rerender({ key: 'three' })
  expect(result.current).toEqual({ status: 'loading', data: ['a'] })
  rerender({ key: null })
  expect(result.current).toEqual({ status: 'idle' })
})

it('reports a failed read as error, never as an empty answer', async () => {
  const { result } = renderHook(() => useDraftPreview<string[]>('k', () => Promise.reject(new Error('down'))))
  await flush()
  expect(result.current).toEqual({ status: 'error' })
  expect(previewHint(result.current, data => data)).toEqual({ state: 'error' })
})

it('maps states to hints; a row the answer does not cover is still loading', () => {
  expect(previewHint({ status: 'idle' }, () => [])).toBeUndefined()
  expect(previewHint({ status: 'loading' }, () => [])).toEqual({ state: 'loading' })
  expect(previewHint({ status: 'ready', data: 1 }, () => [])).toEqual({ state: 'ready', items: [] })
  expect(previewHint({ status: 'ready', data: 1 }, () => undefined)).toEqual({ state: 'loading' })
})
