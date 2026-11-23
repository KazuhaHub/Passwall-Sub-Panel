import type { User } from '@/api/types'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { bytesToGB } from './model'
import { useAdminT } from './useAdminT'

/** Whether an emergency window is open right now. */
export function emergencyActive(user: User): boolean {
  return !!user.emergency_until && new Date(user.emergency_until).getTime() > Date.now()
}

/**
 * While a window is open: until when, how many hours are left, and how much of its quota is gone. The list only
 * says "emergency active"; this is where an admin reads the rest without another round trip.
 */
export function EmergencyWindow({ user }: { user: User }) {
  const t = useAdminT()
  const tz = useSiteStore(s => s.timezone)
  if (!emergencyActive(user) || !user.emergency_until) return null
  const until = new Date(user.emergency_until)
  const hours = Math.max(0, Math.ceil((until.getTime() - Date.now()) / 3600000))
  const used = bytesToGB(user.emergency_used_bytes ?? 0)
  const quota = bytesToGB(user.emergency_quota_bytes ?? 0)
  return (
    <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
      <span>{t('users.field.emergency_window_until', { time: formatDualTz(until.toISOString(), tz), hours })}</span>
      <span className="tabular-nums">
        {quota > 0 ? t('users.field.emergency_window_used_with_quota', { used, quota }) : t('users.field.emergency_window_used_unlimited', { used })}
      </span>
    </div>
  )
}
