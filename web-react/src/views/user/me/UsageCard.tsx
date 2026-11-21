import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import type { Tone } from '@/components/common/tones'
import type { UsageReport } from '@/api/traffic'
import { useSiteStore } from '@/stores/site'
import { bytesToHuman, expiryOf } from './format'
import { Meter } from './Meter'
import { useMeT } from './useMeT'

const GB = 1024 * 1024 * 1024

/**
 * How much traffic is used and left this period, today's use, and when the account expires.
 * Every figure is the one the old card printed; "left" is the one addition, because "how much do I have left" is the
 * question this card exists to answer and subtracting is arithmetic the reader should not have to do.
 */
export function UsageCard({ limitBytes, usage, usageLoading, expireAt, resetPeriod, charged }: {
  limitBytes: number
  usage: UsageReport | null
  /**
   * True only while the usage query's FIRST load is in flight. `usage` is null both then and on a genuine zero
   * reading, so without this the card printed "0.00 / X GB, 0%" for as long as the request took, which reads as
   * an account that has used nothing. A refetch keeps the last figures up instead of blanking them again.
   */
  usageLoading: boolean
  expireAt: string | null
  /** never / monthly / quarterly / yearly: said beside "used" so "this period" is not ambiguous. */
  resetPeriod: string
  /** Some visible line has a non-default multiplier, so the used figure is the charged amount. */
  charged?: boolean
}) {
  const { t } = useMeT()
  // The expiry is shown in the viewer's time zone while the panel sets its cut-offs in its own; say so only when
  // they differ, so a user whose browser zone is not the panel's understands why a date may not match what an admin quoted.
  const panelTz = useSiteStore(s => s.timezone)
  const browserTz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return '' } })()
  const tzDiffers = !!panelTz && !!browserTz && panelTz !== browserTz

  const limitGB = limitBytes / GB
  const usedBytes = usage?.period_used_bytes ?? 0
  const unlimited = limitGB === 0
  const percent = unlimited ? 0 : Math.min(100, (usedBytes / limitBytes) * 100)
  const meterTone: Tone = percent >= 90 ? 'danger' : percent >= 70 ? 'warning' : 'success'

  const expiry = expiryOf(expireAt)
  const expiryTone: Tone = !expiry ? 'neutral' : expiry.expired ? 'danger' : expiry.days <= 7 ? 'warning' : 'neutral'
  const expiryCaption = !expiry ? undefined
    : expiry.expired ? t('profile.expired_days', { days: Math.abs(expiry.days) })
    : expiry.days === 0 ? t('profile.today')
    : t('profile.expire_in', { days: expiry.days })

  return (
    <Card>
      <CardHeader><CardTitle>{t('profile.usage_section')}</CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div>
          <div className="flex items-baseline justify-between gap-3 text-sm text-muted-foreground">
            <span>
              {charged ? t('profile.traffic_used_charged') : t('profile.traffic_used')}
              {resetPeriod && resetPeriod !== 'never' && <span className="ml-1.5 text-xs"> · {t(`profile.reset_period.${resetPeriod}`)}</span>}
            </span>
            <span className="tabular-nums">{usageLoading ? '' : unlimited ? t('profile.unlimited') : `${percent.toFixed(1)}%`}</span>
          </div>
          {usageLoading ? (
            <CardSkeleton lines={2} className="mt-2" />
          ) : (
            <>
              <div className="mt-1 text-2xl font-semibold tabular-nums">
                {unlimited ? bytesToHuman(usedBytes) : `${(usedBytes / GB).toFixed(2)} / ${limitGB.toFixed(0)} GB`}
              </div>
              {!unlimited && (
                <>
                  <Meter className="mt-2" percent={percent} tone={meterTone} label={t('profile.traffic_used')} />
                  <p className="mt-1.5 text-sm text-muted-foreground tabular-nums">{t('profile.traffic_left', { value: bytesToHuman(Math.max(0, limitBytes - usedBytes)) })}</p>
                </>
              )}
            </>
          )}
        </div>
        <KpiGrid className="my-0">
          <KpiTile label={t('profile.today_used')} value={usageLoading ? undefined : bytesToHuman(usage?.today_used_bytes ?? 0)} />
          <KpiTile
            label={t('profile.expire')}
            value={expiry ? expiry.dateStr : t('profile.expire_permanent')}
            tone={expiryTone}
            caption={expiry ? <>{expiryCaption}{tzDiffers && <span className="block">{t('profile.expire_tz_hint', { tz: browserTz })}</span>}</> : undefined}
          />
        </KpiGrid>
      </CardContent>
    </Card>
  )
}
