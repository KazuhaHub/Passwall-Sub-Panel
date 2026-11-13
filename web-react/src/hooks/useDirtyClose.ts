import { useCallback } from 'react'

import { confirm, type ConfirmOpts } from '@/components/ConfirmHost'

/**
 * The guard for a dialog or drawer that holds unsaved changes:
 *
 *   const guard = useDirtyClose(dirty, copy)
 *   <Dialog onClose={() => guard(onClose)} />
 *
 * The ✕, Esc and a click on the backdrop are not navigations, so
 * useLeaveGuard cannot stop them; `guard(close)` asks first and closes only on
 * a yes. With nothing unsaved it closes at once, without a prompt.
 */
export function useDirtyClose(dirty: boolean, copy: ConfirmOpts): (close: () => void) => void {
  return useCallback((close: () => void) => {
    if (!dirty) { close(); return }
    void confirm(copy).then(ok => { if (ok) close() })
  }, [dirty, copy])
}
