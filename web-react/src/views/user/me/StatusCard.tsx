import type { MeProfile } from '@/api/me'
import type { UsageReport } from '@/api/traffic'
import { StatusDot } from '@/components/common/StatusDot'
import { TONE_STYLE, type Tone } from '@/components/common/tones'
import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { expiryOf, isServiceActive, serviceStateOf } from './format'
import { serviceNotice } from './serviceNotice'
import { useMeT } from './useMeT'

/** Days before expiry at which the status turns into a warning; the usage card's expiry tile uses the same number. */
const EXPIRING_DAYS = 7
/** Share of the traffic limit at which the status turns into a warning. */
const NEAR_LIMIT_PERCENT = 90

/**
 * THE ANSWER TO "IS MY SUBSCRIPTION WORKING", in words, at the top of the page.
 *
 * In order of what matters: a service hold (the backend's service axis: expired, traffic used up, blocked client,
 * suspended, ...) with its explanation; emergency access running; about to expire; about to run out of traffic;
 * otherwise working. The held states are the ones the old page printed as coloured banners, with the same texts.
 * "About to ..." is judged from the same numbers the usage card prints, so the two cannot disagree.
 */
export function StatusCard({ profile, usage }: { profile: MeProfile; usage: UsageReport | null }) {
  const { t, i18n } = useMeT()

  const notice = serviceNotice(profile, t, i18n.language)
  const expiry = expiryOf(profile.expire_at)
  const used = usage?.period_used_bytes ?? 0
  const percent = profile.traffic_limit_bytes > 0 ? (used / profile.traffic_limit_bytes) * 100 : 0

  let tone: Tone
  let title: string
  let body: string
  if (notice) {
    ({ tone, title, body } = notice)
  } else if (serviceStateOf(profile) === 'emergency_active') {
    const until = profile.emergency_access?.emergency_until
    tone = 'info'
    title = t('summary.emergency_title')
    body = until ? t('emergency.status_active', { until: new Date(until).toLocaleString() }) : t('summary.emergency_body')
  } else if (expiry && !expiry.expired && expiry.days <= EXPIRING_DAYS) {
    tone = 'warning'
    title = t('summary.expiring_title')
    body = t('summary.expiring_body', {
      when: expiry.days === 0 ? t('profile.today') : t('profile.expire_in', { days: expiry.days }),
      date: expiry.dateStr,
    })
  } else if (percent >= NEAR_LIMIT_PERCENT) {
    tone = 'warning'
    title = t('summary.near_limit_title')
    body = t('summary.near_limit_body', { percent: Math.min(100, Math.floor(percent)) })
  } else if (!isServiceActive(serviceStateOf(profile))) {
    // A state a newer backend added: no claim either way, rather than an all-clear that may be wrong.
    return null
  } else {
    tone = 'success'
    title = t('summary.active_title')
    body = t('summary.active_body')
  }

  return (
    <Card data-testid="service-status" data-tone={tone} className={cn(tone !== 'success' && cn('ring-2', TONE_STYLE[tone].ring))}>
      <CardContent className="flex flex-col gap-1.5">
        <h2 className="text-base font-semibold"><StatusDot tone={tone} label={title} className="text-base" /></h2>
        <p className="text-sm whitespace-pre-wrap text-muted-foreground">{body}</p>
      </CardContent>
    </Card>
  )
}
