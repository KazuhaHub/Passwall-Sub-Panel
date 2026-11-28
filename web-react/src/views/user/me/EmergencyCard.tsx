import type { MeProfile } from '@/api/me'
import { IconDiagnose, IconTime, IconTicket, IconUsage } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { TONE_STYLE } from '@/components/common/tones'
import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { bytesToHuman } from './format'
import { Meter } from '@/components/common/Meter'
import { useMeT } from './useMeT'

type Emergency = MeProfile['emergency_access']

/** What emergency access means for this account right now, as the old card worded it. */
function statusText(t: ReturnType<typeof useMeT>['t'], e: Emergency): string {
  const status = e.status || (e.available ? 'available' : e.remaining <= 0 ? 'no_quota' : 'not_eligible')
  switch (status) {
    case 'available': return t('emergency.status_available', { hours: e.duration_hours })
    case 'active': {
      const until = e.emergency_until ? new Date(e.emergency_until).toLocaleString() : ''
      return until ? t('emergency.status_active', { until }) : t('summary.emergency_body')
    }
    case 'no_quota': return t('emergency.status_no_quota')
    case 'not_eligible': return t('emergency.status_not_eligible')
    case 'disabled': return t('emergency.status_disabled')
    case 'invalid_settings': return t('emergency.status_invalid_settings')
    default: return t('emergency.status_unavailable')
  }
}

/**
 * Emergency access: a time-boxed way back online for an account that has expired or run out of traffic, limited by
 * how many times and for how long. The button is off when no use is left or none is allowed right now, and the
 * sentence above it says which; the page decides nothing about eligibility, the backend's `available` does.
 */
export function EmergencyCard({ emergency, onUse }: {
  emergency: Emergency
  /** Asks, and returns once the profile has been reloaded, so the button is busy for all of it. */
  onUse: () => Promise<void>
}) {
  const { t } = useMeT()
  const quotaActive = (emergency.quota_bytes ?? 0) > 0
  const windowActive = !!emergency.emergency_until && new Date(emergency.emergency_until) > new Date()
  const usedPct = quotaActive ? Math.min(100, (emergency.used_bytes / emergency.quota_bytes) * 100) : 0
  const off = emergency.remaining <= 0 || !emergency.available
  const meta = 'inline-flex items-center gap-1 text-xs text-muted-foreground [&_svg]:size-3.5'
  return (
    <Card data-testid="emergency-access" className={cn('@container', TONE_STYLE.info.tint, TONE_STYLE.info.ring, 'ring-1')}>
      <CardContent className="flex flex-col gap-4">
        {/* The button sits beside the text once the card itself is wide enough, and goes below, full width, when it is not: the card sits in a narrow column at desktop widths too, so the window's width says nothing about it. */}
        <div className="flex flex-col gap-3 @md:flex-row @md:items-start @md:gap-4">
          <div className="flex min-w-0 flex-1 items-start gap-3">
            <IconDiagnose className="mt-0.5 size-5 shrink-0 text-info" />
            <div className="min-w-0 flex-1">
              <h2 className="font-heading text-base leading-snug font-semibold">{t('emergency.title')}</h2>
              <p className="mt-1 text-sm break-words">{statusText(t, emergency)}</p>
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
                <span className={meta}><IconTime />{t('emergency.meta_duration', { hours: emergency.duration_hours })}</span>
                {quotaActive && <span className={meta}><IconUsage />{t('emergency.meta_quota', { total: bytesToHuman(emergency.quota_bytes) })}</span>}
                <span className={meta}><IconTicket />{t('emergency.meta_count', { remaining: emergency.remaining, max: emergency.max_count })}</span>
              </div>
            </div>
          </div>
          <AsyncButton className="w-full @md:w-auto @md:shrink-0" disabled={off} onClick={onUse}>{t('emergency.use')}</AsyncButton>
        </div>
        {windowActive && quotaActive && (
          <div>
            <div className="mb-1 flex justify-between text-xs text-muted-foreground tabular-nums">
              <span>{bytesToHuman(emergency.used_bytes)} / {bytesToHuman(emergency.quota_bytes)}</span>
              <span>{usedPct.toFixed(0)}%</span>
            </div>
            <Meter value={usedPct} tone="info" label={t('emergency.title')} />
          </div>
        )}
      </CardContent>
    </Card>
  )
}
