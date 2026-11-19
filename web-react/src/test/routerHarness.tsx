import type { ReactNode } from 'react'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'

// Mounts a hook under a DATA router (createMemoryRouter): useBlocker, which the
// leave guards need, refuses to run under a plain MemoryRouter. `result.current`
// always holds the hook's latest return value; `router` exposes the history so a
// test can assert on the URL and on Back.
export function renderHookInRouter<T>(hook: () => T, initialEntries: string[] = ['/']) {
  const result = { current: undefined as unknown as T }
  function Probe() {
    result.current = hook()
    return null
  }
  const router = createMemoryRouter([{ path: '*', element: <Probe /> }], { initialEntries })
  const view = render(<RouterProvider router={router} />)
  return { result, router, ...view }
}

export function renderInRouter(ui: ReactNode, initialEntries: string[] = ['/']) {
  const router = createMemoryRouter([{ path: '*', element: ui }], { initialEntries })
  const view = render(<RouterProvider router={router} />)
  return { router, ...view }
}
