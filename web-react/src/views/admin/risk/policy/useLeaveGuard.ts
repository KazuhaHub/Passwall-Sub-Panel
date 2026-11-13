import type { Location } from 'react-router'
import type { ConfirmOpts } from '@/components/ConfirmHost'
import { useLeaveGuard as useGuard } from '@/hooks/useLeaveGuard'

// The guard moved to hooks/useLeaveGuard, with the leave test a parameter.
// This is the policy page's: another page, or another tab of the risk center
// (`tab` leaves 'policy'); the drawer's `user=` and the group card's
// `group=` stay on the page. Kept until the next PR drops this path.
const leavesPolicy = (next: Location, current: Location) =>
  next.pathname !== current.pathname || new URLSearchParams(next.search).get('tab') !== 'policy'

export function useLeaveGuard(dirty: boolean, copy: ConfirmOpts) {
  useGuard(dirty, copy, leavesPolicy)
}
