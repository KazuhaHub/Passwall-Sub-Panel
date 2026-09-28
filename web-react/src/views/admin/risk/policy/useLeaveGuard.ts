import { useEffect, useRef } from 'react'
import { useBlocker } from 'react-router'

import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

/**
 * Stands in front of anything that would drop the policy page's unsaved
 * changes: another page, another tab of the risk center (`tab` leaves
 * 'policy'), a reload or a closed window. What stays on the page is let
 * through — the drawer's `user=` and the group card's `group=` (which asks
 * on its own before it switches a dirty group).
 *
 * useBlocker needs a data router: production mounts one
 * (createBrowserRouter), and the tests that render this page mount
 * createMemoryRouter for the same reason.
 */
export function useLeaveGuard(dirty: boolean, copy: ConfirmOpts) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && (
    nextLocation.pathname !== currentLocation.pathname
    || new URLSearchParams(nextLocation.search).get('tab') !== 'policy'
  ))

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
