import { useSyncExternalStore } from 'react'
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'

// HOW MANY WRITES ARE ON THE WIRE, for the app-wide progress bar.
//
// On a slow link a save, delete or test answered seconds later and nothing on
// screen moved in between; many row actions have no pending state of their own.
// The bar is the floor under all of them: any write in flight long enough to
// notice is shown, whatever button started it.
//
// IT WRAPS THE ADAPTER, NOT THE INTERCEPTORS. The adapter runs once per HTTP
// attempt and its promise settles exactly once — success, failure or abort — so
// begin and end always pair. The interceptor chain does not give that: the 401
// path replays a request through it, and a request aborted before dispatch never
// reaches the response side.
//
// READS ARE NOT COUNTED: every page polls in the background, and a bar that
// tracked those would never go away. A write the operator is not waiting on can
// opt out with `_skipProgress`; a read they are waiting on (a probe behind a
// button) can opt in with `_trackProgress`.

declare module 'axios' {
  export interface AxiosRequestConfig {
    /** Keep this write out of the app-wide progress bar (a background write). */
    _skipProgress?: boolean
    /** Count this read in the app-wide progress bar (a user-started probe). */
    _trackProgress?: boolean
  }
}

let inFlight = 0
const listeners = new Set<() => void>()

function publish(delta: number) {
  inFlight += delta
  listeners.forEach(listener => listener())
}

function counts(config: InternalAxiosRequestConfig): boolean {
  if (config._skipProgress) return false
  if (config._trackProgress) return true
  const method = (config.method ?? 'get').toLowerCase()
  return method !== 'get' && method !== 'head' && method !== 'options'
}

export function trackWrites(adapter: AxiosAdapter): AxiosAdapter {
  return async config => {
    if (!counts(config)) return adapter(config)
    publish(1)
    try {
      return await adapter(config)
    } finally {
      publish(-1)
    }
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** True while at least one counted request is on the wire. */
export function useWriteInProgress(): boolean {
  return useSyncExternalStore(subscribe, () => inFlight > 0, () => false)
}
