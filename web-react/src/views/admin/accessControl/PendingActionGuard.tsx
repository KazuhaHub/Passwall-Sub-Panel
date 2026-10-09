import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from './confirmCopy'
// Idle actions register no blocker. Once an action owns navigation, retain its
// blocker through settlement so queued departures are not lost on unmount.
const PendingActions = createContext<(() => () => void) | null>(null)
export function PendingActionScope({ children }: { children: ReactNode }) {
  const [owners, setOwners] = useState<Set<symbol>>(() => new Set())
  const acquire = useCallback(() => {
    const owner = Symbol()
    setOwners(current => new Set(current).add(owner))
    return () => setOwners(current => { const next = new Set(current); next.delete(owner); return next })
  }, [])
  // React Router admits one active blocker. All pending actions on this page
  // share it, and the last unresolved owner controls its release.
  return <PendingActions.Provider value={acquire}>{children}<RetainedActionGuard hold={owners.size > 0} blockSearch /></PendingActions.Provider>
}
export default function PendingActionGuard({ blockSearch = true, hold = true }: { blockSearch?: boolean; hold?: boolean }) {
  const acquire = useContext(PendingActions)
  useEffect(() => { if (acquire && hold) return acquire() }, [acquire, hold])
  return acquire ? null : <RetainedActionGuard hold={hold} blockSearch={blockSearch} />
}
function RetainedActionGuard({ hold, blockSearch }: { hold: boolean; blockSearch: boolean }) {
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
