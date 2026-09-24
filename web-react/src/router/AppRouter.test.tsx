// @vitest-environment jsdom
import { lazy, Suspense } from 'react'
import { act, render, screen } from '@testing-library/react'
import { createMemoryRouter, Outlet } from 'react-router'
import { describe, expect, it } from 'vitest'
import AppRouter from './AppRouter'

// A view whose chunk never arrives: a slow network, frozen.
const NeverLoadedView = lazy(() => new Promise<{ default: () => null }>(() => {}))

function Layout() {
  return <Suspense fallback={<p>loading view</p>}><Outlet /></Suspense>
}

describe('AppRouter', () => {
  // CLICKING MUST CHANGE THE PAGE AT ONCE, EVEN WHEN ITS CODE HAS NOT ARRIVED.
  // The router wraps navigation in a React transition by default, and a
  // transition keeps the OLD page on screen while the new one suspends — so on a
  // slow network a click showed nothing at all until the chunk finished, and the
  // layout's loading fallback was never seen.
  it('shows the layout fallback immediately while a view chunk is still loading', async () => {
    const router = createMemoryRouter([{
      element: <Layout />,
      children: [
        { path: '/a', element: <p>page A</p> },
        { path: '/b', element: <NeverLoadedView /> },
      ],
    }], { initialEntries: ['/a'] })
    render(<AppRouter router={router} />)
    expect(await screen.findByText('page A')).toBeTruthy()

    await act(async () => { await router.navigate('/b') })

    expect(screen.getByText('loading view')).toBeTruthy()
    // React keeps the previous content mounted but hidden under the fallback.
    const previous = screen.queryByText('page A')
    expect(previous === null || previous.style.display === 'none').toBe(true)
  })
})
