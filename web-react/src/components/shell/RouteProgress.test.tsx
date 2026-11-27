// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react'
import { Suspense, lazy } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { ROUTE_PROGRESS_DELAY_MS, RouteFallback, RouteLoadingBar } from './RouteProgress'

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('the route progress bar', () => {
  it('stays hidden until the delay has passed', () => {
    render(<RouteLoadingBar />)
    expect(screen.queryByRole('progressbar')).toBeNull()
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS - 1) })
    expect(screen.queryByRole('progressbar')).toBeNull()
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.getByRole('progressbar')).toBeTruthy()
  })

  it('is never drawn when the route arrives inside the delay', () => {
    const { unmount } = render(<RouteLoadingBar />)
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS - 10) })
    unmount()
    act(() => { vi.advanceTimersByTime(1000) })
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('is mounted by a suspended route and gone once the chunk resolves', async () => {
    let resolve!: (m: { default: () => JSX.Element }) => void
    const Page = lazy(() => new Promise<{ default: () => JSX.Element }>(r => { resolve = r }))
    render(<Suspense fallback={<RouteFallback />}><Page /></Suspense>)
    expect(screen.getByRole('status')).toBeTruthy() // the page skeleton shows at once
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS) })
    expect(screen.getByRole('progressbar')).toBeTruthy()
    await act(async () => { resolve({ default: () => <p>loaded</p> }) })
    expect(screen.getByText('loaded')).toBeTruthy()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
})
