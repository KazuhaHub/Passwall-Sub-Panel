// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react'
import { Suspense, lazy, type ReactElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { ROUTE_PROGRESS_DELAY_MS, RouteFallback, RouteProgressHost } from './RouteProgress'

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers() })

function mountSuspended() {
  let resolve!: (m: { default: () => ReactElement }) => void
  const Page = lazy(() => new Promise<{ default: () => ReactElement }>(r => { resolve = r }))
  render(
    <RouteProgressHost>
      <main data-testid="main"><Suspense fallback={<RouteFallback />}><Page /></Suspense></main>
    </RouteProgressHost>,
  )
  return { load: async () => { await act(async () => { resolve({ default: () => <p>loaded</p> }) }) } }
}

describe('the route progress bar', () => {
  it('stays hidden until the delay has passed, while the skeleton shows at once', () => {
    mountSuspended()
    expect(screen.getByRole('status')).toBeTruthy()
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS - 1) })
    expect(screen.queryByRole('progressbar')).toBeNull()
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.getByRole('progressbar')).toBeTruthy()
  })

  it('is never drawn when the route arrives inside the delay', async () => {
    const { load } = mountSuspended()
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS - 10) })
    await load()
    act(() => { vi.advanceTimersByTime(1000) })
    expect(screen.getByText('loaded')).toBeTruthy()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('is drawn outside the page content (an animated <main> would re-anchor a fixed bar) and goes once the chunk resolves', async () => {
    const { load } = mountSuspended()
    act(() => { vi.advanceTimersByTime(ROUTE_PROGRESS_DELAY_MS) })
    const bar = screen.getByRole('progressbar')
    expect(screen.getByTestId('main').contains(bar)).toBe(false)
    await load()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('does nothing without a host (a fallback shown outside a shell)', () => {
    render(<RouteFallback />)
    act(() => { vi.advanceTimersByTime(1000) })
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
})
