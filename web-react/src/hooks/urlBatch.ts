import { useCallback, useContext, useRef } from 'react'
import { UNSAFE_DataRouterContext, useLocation, useNavigate } from 'react-router'

export type UrlHistory = 'replace' | 'push'

export interface UrlOpContext {
  /** The location the batch is applied to. */
  location: { pathname: string; search: string; state: unknown }
  /** Ask for a new history entry for this batch (any op asking wins over replace). */
  push: () => void
  /** Merge into the history state of the entry the batch writes. */
  state: (patch: Record<string, unknown>) => void
}
export type UrlOp = (params: URLSearchParams, ctx: UrlOpContext) => void

interface RouterLike {
  state: { location: { pathname: string; search: string; hash: string; state: unknown } }
  navigate: (to: unknown, opts?: unknown) => Promise<void>
}

interface Batch {
  ops: { op: UrlOp; history: UrlHistory }[]
  scheduled: boolean
  /** A navigation we started has not landed yet: build on what we wrote, not on router.state. */
  inflight: boolean
  written?: { pathname: string; search: string; hash: string; state: unknown }
}

// One batch per router instance: every hook on the page writes through it.
const batches = new WeakMap<object, Batch>()

function flush(router: RouterLike, batch: Batch) {
  batch.scheduled = false
  const ops = batch.ops
  batch.ops = []
  const base = batch.inflight && batch.written ? batch.written : router.state.location
  const params = new URLSearchParams(base.search)
  let push = false
  let state: Record<string, unknown> = { ...((base.state as object | null) ?? {}) }
  const ctx: UrlOpContext = {
    location: base,
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
  void router.navigate({ pathname: base.pathname, search, hash: base.hash }, { replace: !push, state })
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
 * This needs a data router (createBrowserRouter), which is what the app mounts;
 * the router instance is the batch key. Without one, each hook still composes its
 * own writes but cannot see another hook's.
 */
export function useUrlWriter(): (op: UrlOp, history: UrlHistory) => void {
  const router = useContext(UNSAFE_DataRouterContext)?.router as RouterLike | undefined
  const location = useLocation()
  const navigate = useNavigate()
  const fallback = useRef({ search: location.search, location, navigate })
  fallback.current = { search: fallback.current.search === location.search ? fallback.current.search : location.search, location, navigate }

  return useCallback((op: UrlOp, history: UrlHistory) => {
    if (router) {
      let batch = batches.get(router)
      if (!batch) { batch = { ops: [], scheduled: false, inflight: false }; batches.set(router, batch) }
      batch.ops.push({ op, history })
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
