import { useEffect, useRef } from 'react'
import { useBlocker, type Location } from 'react-router'

import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

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
 * useBlocker needs a data router: production mounts one
 * (createBrowserRouter), and the tests that render a guarded page mount
 * createMemoryRouter for the same reason.
 */
export function useLeaveGuard(
  dirty: boolean,
  copy: ConfirmOpts,
  leaves: (next: Location, current: Location) => boolean,
) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && leaves(nextLocation, currentLocation))

  // The dialog's words, read when a navigation is blocked. A ref, so the
  // effect below runs once per blocked navigation rather than once per
  // render while the dialog is open.
  const words = useRef(copy)
  useEffect(() => { words.current = copy })

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    let live = true
    void confirm(words.current).then(ok => {
      if (!live) return
      if (ok) blocker.proceed()
      else blocker.reset()
    })
    return () => { live = false }
  }, [blocker])

  // A reload or a closed window: the browser's own prompt, the only one a
  // page may show there.
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])
}
