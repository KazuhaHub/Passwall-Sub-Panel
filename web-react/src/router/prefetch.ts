import { viewLoaders, type ViewLoader } from './viewModules'

// PREFETCHING VIEW CHUNKS SO A CLICK DOES NOT WAIT ON THE NETWORK.
//
// Every view is its own chunk, fetched on first visit. On a slow link that first
// visit was a multi-second wait, so the sidebar now warms chunks ahead of the
// click: the item under the pointer or focus at once (the operator is about to
// open it), and everything the role can reach one at a time while the browser is
// idle. Hashed chunks are cached for a year, so this is paid once per release.

interface PrefetcherOptions {
  /** Runs cb when the browser is idle; returns a cancel function. */
  scheduleIdle?: (cb: () => void) => () => void
  /** True when the browser asks to save data; background loads are skipped. */
  saveData?: () => boolean
}

function defaultScheduleIdle(cb: () => void): () => void {
  if (typeof window.requestIdleCallback === 'function') {
    const handle = window.requestIdleCallback(cb, { timeout: 10_000 })
    return () => window.cancelIdleCallback(handle)
  }
  // Safari has no requestIdleCallback; wait long enough for the page's own
  // first requests to get ahead of the background ones.
  const handle = window.setTimeout(cb, 2_000)
  return () => window.clearTimeout(handle)
}

function defaultSaveData(): boolean {
  const connection = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection
  return connection?.saveData === true
}

export function createViewPrefetcher(loaders: Record<string, ViewLoader>, options: PrefetcherOptions = {}) {
  const scheduleIdle = options.scheduleIdle ?? defaultScheduleIdle
  const saveData = options.saveData ?? defaultSaveData
  const started = new Map<string, Promise<boolean>>()

  // Resolves true once the chunk is in, false when the path is unknown or the
  // load failed. It never rejects: a prefetch is a hint, and a failed one must
  // not surface — the click that follows loads the view the ordinary way. A
  // failure is forgotten so the next hint may try again.
  function prefetch(path: string): Promise<boolean> {
    const load = loaders[path]
    if (!load) return Promise.resolve(false)
    let pending = started.get(path)
    if (!pending) {
      pending = load().then(() => true, () => {
        started.delete(path)
        return false
      })
      started.set(path, pending)
    }
    return pending
  }

  // ONE VIEW PER IDLE PERIOD, IN ORDER, so background loads never crowd the
  // page's own requests; and the queue stops at the first failure, because on a
  // dead link every remaining attempt would fail the same way.
  function prefetchWhenIdle(paths: readonly string[]): () => void {
    if (saveData()) return () => {}
    const queue = [...paths]
    let cancelled = false
    let cancelIdle = () => {}
    const next = () => {
      const path = queue.shift()
      if (cancelled || path === undefined) return
      cancelIdle = scheduleIdle(() => {
        void prefetch(path).then(loaded => { if (loaded) next() })
      })
    }
    next()
    return () => {
      cancelled = true
      cancelIdle()
    }
  }

  return { prefetch, prefetchWhenIdle }
}

const prefetcher = createViewPrefetcher(viewLoaders)

export function prefetchView(path: string): void {
  void prefetcher.prefetch(path)
}

export function prefetchViewsWhenIdle(paths: readonly string[]): () => void {
  return prefetcher.prefetchWhenIdle(paths)
}
