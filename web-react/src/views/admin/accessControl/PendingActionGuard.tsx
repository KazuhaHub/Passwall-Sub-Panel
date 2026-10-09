import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useCallback, useEffect, useState } from 'react'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from './confirmCopy'
// Idle actions register no blocker. Once an action owns navigation, retain its
// blocker through settlement so queued departures are not lost on unmount.
export default function PendingActionGuard({ blockSearch = true, hold = true }: { blockSearch?: boolean; hold?: boolean }) {
  const [retained, setRetained] = useState(hold)
  useEffect(() => { if (hold) setRetained(true) }, [hold])
  const release = useCallback(() => setRetained(false), [])
  return hold || retained ? <HeldActionGuard hold={hold} blockSearch={blockSearch} onReleased={release} /> : null
}
function HeldActionGuard({ hold, blockSearch, onReleased }: { hold: boolean; blockSearch: boolean; onReleased: () => void }) {
  const { t } = useAccessTranslation('admin')
  const state = useLeaveGuard(false, discardSettingsCopy(t), blockSearch ? (next, current) => next.pathname !== current.pathname || next.search !== current.search : undefined, hold)
  useEffect(() => { if (!hold && state === 'unblocked') onReleased?.() }, [hold, state, onReleased])
  return null
}
