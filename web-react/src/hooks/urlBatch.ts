import { useCallback, useContext, useRef } from 'react'
import { UNSAFE_DataRouterContext, useLocation, useNavigate } from 'react-router'

export type UrlHistory = 'replace' | 'push'

export interface UrlOpContext {
  /** The location the batch is applied to (pathname WITHOUT the router's basename). */
  location: { pathname: string; search: string; state: unknown }
  /** Ask for a new history entry for this batch (any op asking wins over replace). */
  push: () => void
  /** Merge into the history state of the entry the batch writes. */
  state: (patch: Record<string, unknown>) => void
}
export type UrlOp = (params: URLSearchParams, ctx: UrlOpContext) => void

interface Loc { pathname: string; search: string; hash: string; state: unknown }
interface RouterLike {
  basename?: string
  state: { location: Loc; navigation: { state: string; location?: Loc } }
  navigate: (to: unknown, opts?: unknown) => Promise<void>
}

interface Batch {
  ops: { op: UrlOp; history: UrlHistory; origin: string }[]
  scheduled: boolean
  /** A navigation we started has not landed yet: build on what we wrote, not on router.state. */
  inflight: boolean
  written?: Loc
}

// One batch per router instance: every hook on the page writes through it.
const batches = new WeakMap<object, Batch>()

// router.state.location.pathname still carries the basename (panel_path), and
// router.navigate() adds it again: a path handed back must have it taken off.
function stripBasename(router: RouterLike, pathname: string): string {
  const base = router.basename
  if (!base || base === '/') return pathname
  if (pathname === base) return '/'
  return pathname.startsWith(`${base}/`) ? pathname.slice(base.length) : pathname
}

/** Where the router is, or is about to be: a navigation under way counts. */
function landing(router: RouterLike, batch: Batch): Loc {
  if (batch.inflight && batch.written) return batch.written
  const nav = router.state.navigation
  return nav.state !== 'idle' && nav.location ? nav.location : router.state.location
}

function flush(router: RouterLike, batch: Batch) {
  batch.scheduled = false
  const all = batch.ops
  batch.ops = []
  const base = landing(router, batch)
  const basePath = stripBasename(router, base.pathname)
  // A write belongs to the route it was made on. If the location is no longer that
  // route (a navigate() in the same handler, before or after the write), the write
  // is dropped: carrying `q=x` or `row=12` from one page onto another would open
  // the wrong thing there.
  const ops = all.filter(o => o.origin === basePath)
  if (!ops.length) return
  const params = new URLSearchParams(base.search)
  let push = false
  let state: Record<string, unknown> = { ...((base.state as object | null) ?? {}) }
  const ctx: UrlOpContext = {
    location: { pathname: basePath, search: base.search, state: base.state },
    push: () => { push = true },
    state: patch => { state = { ...state, ...patch } },
  }
  for (const { op, history } of ops) {
    if (history === 'push') push = true
    op(params, ctx)
  }
  const text = params.toString()
  const search = text ? `?${text}` : ''
  const stateChanged = JSON.stringify(state) !== JSON.stringify((base.state as object | null) ?? {})
  if (search === base.search && !stateChanged && !push) return
  batch.written = { pathname: base.pathname, search, hash: base.hash, state }
  batch.inflight = true
  void router.navigate({ pathname: basePath, search, hash: base.hash }, { replace: !push, state })
    .finally(() => { batch.inflight = false })
}

/**
 * The single way the URL hooks write the query string.
 *
 * Writes made in the same tick, from any number of hooks, are collected and
 * applied together to the LIVE location when the tick ends, so two hooks
 * changing different keys both land, and the result is one history entry: a push
 * if any write asked for one, otherwise a replace. A hook working from its own
 * last render would lose all but the last writer (a filter and a page reset made
 * together, a row opened while a keyword changes).
 *
 * It needs a data router (createBrowserRouter), which is what the app mounts, and
 * the router instance is the batch key. Without one, writes from different hooks
 * cannot see each other, so in development it throws instead of quietly losing
 * them; a production build falls back to per-hook composition.
 */
export function useUrlWriter(): (op: UrlOp, history: UrlHistory) => void {
  const router = useContext(UNSAFE_DataRouterContext)?.router as RouterLike | undefined
  if (!router && import.meta.env.DEV) {
    throw new Error('The URL hooks (useUrlState, useUrlStates, useUrlSelection) need a data router: mount the app with createBrowserRouter, or createMemoryRouter in a test.')
  }
  const location = useLocation()
  const navigate = useNavigate()
  const fallback = useRef({ search: location.search, location, navigate })
  fallback.current = { search: fallback.current.search === location.search ? fallback.current.search : location.search, location, navigate }

  return useCallback((op: UrlOp, history: UrlHistory) => {
    if (router) {
      let batch = batches.get(router)
      if (!batch) { batch = { ops: [], scheduled: false, inflight: false }; batches.set(router, batch) }
      // The route this write is made on: the one the calling component rendered for
      // (useLocation, so already without the basename), not wherever the router is
      // by now: a navigate() in the same handler may already have moved it.
      batch.ops.push({ op, history, origin: fallback.current.location.pathname })
      if (!batch.scheduled) {
        batch.scheduled = true
        const b = batch
        queueMicrotask(() => flush(router, b))
      }
      return
    }
    const f = fallback.current
    const params = new URLSearchParams(f.search)
    let push = history === 'push'
    let state = { ...((f.location.state as object | null) ?? {}) }
    op(params, { location: f.location, push: () => { push = true }, state: p => { state = { ...state, ...p } } })
    const text = params.toString()
    const search = text ? `?${text}` : ''
    if (search === f.search) return
    f.search = search
    void f.navigate({ search }, { replace: !push, state })
  }, [router])
}
