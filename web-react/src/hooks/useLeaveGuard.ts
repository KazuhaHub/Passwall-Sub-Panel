import { useEffect, useRef } from 'react'
import { useBlocker, type Location } from 'react-router'

import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

/**
 * Confirms navigation that would discard unsaved changes. By default only
 * pathname changes leave the editor; callers may include their own tab
 * boundary while allowing drawer and filter changes on the same page.
 *
 * useBlocker needs a data router: production mounts one
 * (createBrowserRouter), and tests mount
 * createMemoryRouter for the same reason.
 */
export function useLeaveGuard(dirty: boolean, copy: ConfirmOpts, leaves: (next: Location, current: Location) => boolean = (next, current) => next.pathname !== current.pathname, hold = false) {
  // A save or another confirmation owns the dialog while hold is true. Keep
  // the requested navigation blocked without opening a competing prompt.
  const blocker = useBlocker(({ currentLocation, nextLocation }) => (dirty || hold) && leaves(nextLocation, currentLocation))

  // The dialog's words, read when a navigation is blocked. A ref, so the
  // effect below runs once per blocked navigation rather than once per
  // render while the dialog is open.
  const words = useRef(copy)
  useEffect(() => { words.current = copy })

  useEffect(() => {
    if (blocker.state !== 'blocked' || hold) return
    if (!dirty) { blocker.proceed(); return }
    let live = true
    void confirm(words.current).then(ok => {
      if (!live) return
      if (ok) blocker.proceed()
      else blocker.reset()
    })
    return () => { live = false }
  }, [blocker, dirty, hold])

  // A reload or a closed window: the browser's own prompt, the only one a
  // page may show there.
  useEffect(() => {
    if (!dirty && !hold) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty, hold])
  return blocker.state
}
