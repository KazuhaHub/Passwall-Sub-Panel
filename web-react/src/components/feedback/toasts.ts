import { useSyncExternalStore } from 'react'

import type { Severity, ToastInput, ToastStore } from './toastStore'

// The module-level doors into the toast layer, for code that is not React (axios
// interceptors, utilities). The state itself is NOT here: it lives in a store the host
// creates when it mounts, so nothing leaks from one test or one mount to the next. A
// push that arrives before any host exists is buffered and handed over when one attaches.

let active: ToastStore | null = null
const buffer: ToastInput[] = []
// A host that never mounts (a test importing a module that pushes) must not grow this
// without bound; the oldest are dropped first.
const BUFFER_LIMIT = 100

/** Raises a toast. `key` and `action` are for callers with more to say than a message. */
export function pushToast(input: ToastInput): void {
  if (!input.message) return
  if (active) { active.push(input); return }
  buffer.push(input)
  if (buffer.length > BUFFER_LIMIT) buffer.shift()
}

/** The signature the whole app has always called. */
export function pushSnack(message: string, severity: Severity = 'info'): void {
  pushToast({ message, severity })
}

/** Called by the host on mount: flushes what was buffered, once. Returns the detach. */
export function attachToastStore(store: ToastStore): () => void {
  active = store
  const unsubscribe = store.subscribe(() => {
    const { unread, unreadErrors } = store.getState()
    setLogSummary({ unread, unreadErrors })
  })
  for (const input of buffer.splice(0)) store.push(input)
  return () => {
    unsubscribe()
    if (active === store) { active = null; setLogSummary(NONE) }
  }
}

// THE LOG'S DOORS. The log panel is owned by the host, but it must be reachable after the
// summary toast that points at it is gone (it can be dismissed, or expire when it holds
// nothing urgent), so the shell's top bar opens it through these.
let openLogFn: (() => void) | null = null

/** Called by the host: registers the function that opens the panel. */
export function registerToastLogOpener(fn: () => void): () => void {
  openLogFn = fn
  return () => { if (openLogFn === fn) openLogFn = null }
}

/** Opens the log of recent messages, including the ones that were never shown as toasts. */
export function openToastLog(): void {
  openLogFn?.()
}

export interface ToastLogSummary {
  /** Messages the user has not seen as toasts. */
  unread: number
  unreadErrors: number
}

const NONE: ToastLogSummary = { unread: 0, unreadErrors: 0 }
let logSummary: ToastLogSummary = NONE
const summaryListeners = new Set<() => void>()

function setLogSummary(next: ToastLogSummary) {
  if (next.unread === logSummary.unread && next.unreadErrors === logSummary.unreadErrors) return
  logSummary = next
  summaryListeners.forEach(fn => fn())
}

/** For the top bar: how many messages were never shown, and how many of those are errors. */
export function useToastLogSummary(): ToastLogSummary {
  return useSyncExternalStore(
    fn => { summaryListeners.add(fn); return () => { summaryListeners.delete(fn) } },
    () => logSummary,
  )
}
