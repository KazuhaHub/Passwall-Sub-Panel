import type { ReactNode } from 'react'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'

/** The base paths every URL-hook test runs under: none, and a panel_path prefix. */
export const BASENAMES = ['', '/panel'] as const

// Mounts a hook under a DATA router (createMemoryRouter): useBlocker, which the
// leave guards need, refuses to run under a plain MemoryRouter, and the URL hooks
// batch their writes per data router. `entries` are app-relative ("/list?q=a");
// with a `basename` (the app's panel_path) the harness prefixes them, so a hook
// that builds a path from router.state.location without stripping the basename
// ends up on a route that does not exist and the test sees it.
// `result.current` always holds the hook's latest return value; `router` exposes
// the history so a test can assert on the URL and on Back; `where()` is the
// location with the basename taken off.
export function renderHookInRouter<T>(hook: () => T, entries: string[] = ['/'], basename = '') {
  const result = { current: undefined as unknown as T }
  function Probe() {
    result.current = hook()
    return null
  }
  const router = createMemoryRouter([{ path: '*', element: <Probe /> }], {
    initialEntries: entries.map(e => `${basename}${e}`),
    basename: basename || undefined,
  })
  const view = render(<RouterProvider router={router} />)
  const where = () => {
    const { pathname, search } = router.state.location
    return { pathname: basename && pathname.startsWith(basename) ? pathname.slice(basename.length) || '/' : pathname, search }
  }
  return { result, router, where, ...view }
}

export function renderInRouter(ui: ReactNode, entries: string[] = ['/'], basename = '') {
  const router = createMemoryRouter([{ path: '*', element: ui }], {
    initialEntries: entries.map(e => `${basename}${e}`),
    basename: basename || undefined,
  })
  const view = render(<RouterProvider router={router} />)
  return { router, ...view }
}
