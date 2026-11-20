import { useCallback, useEffect, useRef, useState } from 'react'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { useNodesT } from '../shared/useNodesT'

/**
 * Stands in front of everything that would drop the unsaved edits of a line editor that sits in a sheet (design 4.9):
 * the ✕, Esc and a click outside (`close`, which asks first), another page, a Back that closes the sheet (`param` is the address parameter that names the open line), and a
 * reload. `leave` is for a save or an answered question: it lets the next change of the address through
 * without asking, because the edits are gone or kept on purpose.
 *
 * The router guard stops a change of page or of the open line (`param`) only. The sheet's own mode (`edit=`) is not a
 * reason to ask: going back to the view of the same line is the sheet's Back button, which asks through `close`.
 */
export function useGuardedEditor(param = 'line') {
  const t = useNodesT()
  const [dirty, setDirty] = useState(false)
  const bypass = useRef(false)
  const copy = discardChangesConfirm(t)
  const guard = useDirtyClose(dirty, copy)
  useLeaveGuard(dirty, copy, (next, current) => {
    if (bypass.current) return false
    if (next.pathname !== current.pathname) return true
    return new URLSearchParams(next.search).get(param) !== new URLSearchParams(current.search).get(param)
  })
  // A new edit session asks again, whatever was answered before.
  useEffect(() => { if (dirty) bypass.current = false }, [dirty])

  /** Runs `then` now when nothing is unsaved, otherwise after the person agrees to drop the edits. */
  const close = useCallback((then: () => void) => guard(() => { bypass.current = true; then() }), [guard])
  const leave = useCallback((then: () => void) => { bypass.current = true; then() }, [])
  return { dirty, setDirty, close, leave }
}
