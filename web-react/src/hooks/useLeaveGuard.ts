import { useContext, useEffect, useRef } from 'react'
import { UNSAFE_DataRouterContext, type Location } from 'react-router'

import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

// ONE BLOCKER PER ROUTER, ONE PROMPT. React Router allows a single navigation blocker at a time; a page, a sheet on it
// and a tab kept mounted each asking for their own left all but one of them silently doing nothing (and a console
// warning per render). So no component calls `useBlocker`: every guard REGISTERS its state here, and one blocker per
// router (created when the first guard registers, removed when the last one goes) asks the registry. It blocks when any
// registered guard is dirty and would lose its draft by this navigation, and shows ONE prompt naming them all.
// `beforeunload` is likewise a single listener over the same registry.

/** What the registry reads from one guard, at the moment it is asked (not when it was rendered). */
export interface GuardEntry {
  dirty: boolean
  leaves: (next: Location, current: Location) => boolean
  /** The prompt's words. Several guards that block one navigation share a prompt: the first one's title and buttons, every message. */
  copy?: ConfirmOpts
  /** A guard that asks its own way (a plain window.confirm, say) instead of the shared prompt. */
  ask?: () => Promise<boolean>
}
type EntryRef = { current: GuardEntry }

const entries = new Set<EntryRef>()
const dirtyEntries = () => [...entries].filter(e => e.current.dirty)

// ---- beforeunload: one listener while anything is registered ------------------------------------------------------

function onBeforeUnload(e: BeforeUnloadEvent) {
  if (dirtyEntries().length > 0) e.preventDefault()
}
function trackEntry(ref: EntryRef): () => void {
  if (entries.size === 0) window.addEventListener('beforeunload', onBeforeUnload)
  entries.add(ref)
  return () => {
    entries.delete(ref)
    if (entries.size === 0) window.removeEventListener('beforeunload', onBeforeUnload)
  }
}

// ---- the blocker of one data router --------------------------------------------------------------------------------

type DataRouter = NonNullable<React.ContextType<typeof UNSAFE_DataRouterContext>>

interface Host { users: number; stop: () => void }
const hosts = new WeakMap<object, Host>()
let hostId = 0

const stripBasename = (path: string, basename: string) => (basename !== '/' && path.startsWith(basename) ? path.slice(basename.length) || '/' : path)

function promptFor(blocking: EntryRef[]): Promise<boolean> {
  const custom = blocking.find(e => e.current.ask)
  if (custom) return custom.current.ask!()
  const copies = blocking.map(e => e.current.copy).filter((c): c is ConfirmOpts => !!c)
  if (copies.length === 0) return Promise.resolve(true)
  const messages = [...new Set(copies.map(c => c.message).filter(Boolean))]
  return confirm({ ...copies[0], message: messages.join('\n') })
}

function startHost({ router, basename }: DataRouter): Host {
  const key = `psp-leave-guard-${++hostId}`
  const leaving = (current: Location, next: Location) => dirtyEntries().filter(e => e.current.leaves(next, current))
  router.getBlocker(key, ({ currentLocation, nextLocation }) => {
    const current = { ...currentLocation, pathname: stripBasename(currentLocation.pathname, basename) }
    const next = { ...nextLocation, pathname: stripBasename(nextLocation.pathname, basename) }
    return leaving(current, next).length > 0
  })
  let asking = false
  const unsubscribe = router.subscribe(state => {
    const blocker = state.blockers.get(key)
    if (blocker?.state !== 'blocked' || asking) return
    asking = true
    const current = { ...router.state.location, pathname: stripBasename(router.state.location.pathname, basename) }
    const next = { ...blocker.location, pathname: stripBasename(blocker.location.pathname, basename) }
    const blocking = leaving(current, next)
    void promptFor(blocking.length ? blocking : dirtyEntries())
      .then(ok => { asking = false; if (ok) blocker.proceed(); else blocker.reset() })
  })
  return { users: 0, stop: () => { unsubscribe(); router.deleteBlocker(key) } }
}

function joinRouter(data: DataRouter): () => void {
  let host = hosts.get(data.router)
  if (!host) { host = startHost(data); hosts.set(data.router, host) }
  host.users++
  return () => {
    const h = hosts.get(data.router)
    if (!h) return
    if (--h.users === 0) { h.stop(); hosts.delete(data.router) }
  }
}

/**
 * Registers one guard with the shared registry for as long as the caller is mounted. `read` is called when a navigation
 * or an unload asks, so the answer always reflects the latest render; the guard needs no re-registration when `dirty` changes.
 * Without a data router (a plain MemoryRouter, a test of an embedded component) there is no blocker to attach to, so only
 * the reload prompt applies: a deliberate no-op for navigation, not an error.
 */
export function useGuardRegistration(entry: GuardEntry) {
  const data = useContext(UNSAFE_DataRouterContext)
  const ref = useRef(entry)
  useEffect(() => { ref.current = entry })
  // Also assigned during render, so a navigation that starts before the effect runs still sees the latest state.
  ref.current = entry
  useEffect(() => {
    const untrack = trackEntry(ref)
    const leave = data ? joinRouter(data) : null
    return () => { leave?.(); untrack() }
  }, [data])
}

/**
 * Stands in front of anything that would drop a page's unsaved changes:
 * another page, another tab (whatever `leaves` says), a reload or a closed
 * window. What stays on the page is let through — `leaves` is asked with the
 * location the navigation starts from and the one it goes to, and answers
 * whether the draft is lost by it.
 *
 * It guards ROUTE changes and reloads only. A dialog or a drawer closed by
 * its ✕, Esc or a click on the backdrop is not a navigation: that is
 * useDirtyClose.
 *
 * Any number of guards may be mounted at once (a page and its sheet, kept tabs): see the registry above. A data router
 * (createBrowserRouter) is needed for the navigation prompt; production mounts one and the tests that render a guarded
 * page mount createMemoryRouter.
 */
export function useLeaveGuard(
  dirty: boolean,
  copy: ConfirmOpts,
  leaves: (next: Location, current: Location) => boolean,
) {
  useGuardRegistration({ dirty, leaves, copy })
}
