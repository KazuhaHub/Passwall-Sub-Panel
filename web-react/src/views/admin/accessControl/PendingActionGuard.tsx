import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useEffect } from 'react'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from './confirmCopy'
// Mount only while an action owns its confirmation/write. Editors mount their
// own guard; keeping inactive blockers out of the router avoids competing guards.
export default function PendingActionGuard({ blockSearch = false, hold = true, onReleased }: { blockSearch?: boolean; hold?: boolean; onReleased?: () => void }) {
  const { t } = useAccessTranslation('admin')
  const state = useLeaveGuard(false, discardSettingsCopy(t), blockSearch ? (next, current) => next.pathname !== current.pathname || next.search !== current.search : undefined, hold)
  // Retain the blocker through release so a queued navigation can proceed
  // before this transient guard is unmounted.
  useEffect(() => { if (!hold && state === 'unblocked') onReleased?.() }, [hold, state, onReleased])
  return null
}
