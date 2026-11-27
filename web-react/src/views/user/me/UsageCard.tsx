import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { TONE_STYLE, type Tone } from '@/components/common/tones'
import type { UsageReport } from '@/api/traffic'
import { cn } from '@/lib/utils'
import { useSiteStore } from '@/stores/site'
import { bytesToHuman, expiryOf } from './format'
import { Meter } from '@/components/common/Meter'
import { useMeT } from './useMeT'

const GB = 1024 * 1024 * 1024

/**
 * How much traffic is used and left this period, today's use, and when the account expires.
 * The long-standing layout: used and total with a bar, then today and the expiry side by side. "Left" is the one addition
 * kept from the rebuild, because "how much do I have left" is the question this card exists to answer.
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
  // The bar is the brand colour while all is well and turns warning, then danger, as the limit nears; the figures beside
  // it say the same in words.
  const meterTone: Tone | undefined = percent >= 90 ? 'danger' : percent >= 70 ? 'warning' : undefined

  const expiry = expiryOf(expireAt)
  const expiryTone: Tone | undefined = !expiry ? undefined : expiry.expired ? 'danger' : expiry.days <= 7 ? 'warning' : undefined
  const expiryCaption = !expiry ? undefined
    : expiry.expired ? t('profile.expired_days', { days: Math.abs(expiry.days) })
    : expiry.days === 0 ? t('profile.today')
    : t('profile.expire_in', { days: expiry.days })

  return (
    <Card>
      <CardHeader><CardTitle>{t('profile.usage_section')}</CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-6">
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
              <div className="mt-2 text-[1.375rem] leading-tight font-medium tabular-nums">
                {unlimited ? bytesToHuman(usedBytes) : `${(usedBytes / GB).toFixed(2)} / ${limitGB.toFixed(0)} GB`}
              </div>
              {!unlimited && (
                <>
                  <Meter className="mt-2" value={percent} tone={meterTone} barClassName={meterTone ? undefined : 'bg-brand'} label={t('profile.traffic_used')} />
                  <p className="mt-1.5 text-sm text-muted-foreground tabular-nums">{t('profile.traffic_left', { value: bytesToHuman(Math.max(0, limitBytes - usedBytes)) })}</p>
                </>
              )}
            </>
          )}
        </div>
        {/* Today and the expiry side by side, plainly: a label, a figure, and under the expiry its own thin bar. */}
        <div className="grid grid-cols-1 gap-6 sm:grid-cols-2">
          <div>
            <div className="text-sm text-muted-foreground">{t('profile.today_used')}</div>
            <div className="mt-1 text-lg font-medium tabular-nums">{usageLoading ? t('common:status.loading') : bytesToHuman(usage?.today_used_bytes ?? 0)}</div>
          </div>
          <div>
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-sm text-muted-foreground">{t('profile.expire')}</span>
              {expiryCaption && <span className={cn('text-xs tabular-nums', expiryTone ? TONE_STYLE[expiryTone].ink : 'text-muted-foreground')}>{expiryCaption}</span>}
            </div>
            <div className="mt-1 text-lg font-medium tabular-nums">{expiry ? expiry.dateStr : t('profile.expire_permanent')}</div>
            {expiry && tzDiffers && <div className="mt-0.5 text-[0.6875rem] text-muted-foreground">{t('profile.expire_tz_hint', { tz: browserTz })}</div>}
            {expiry && (
              <Meter
                className="mt-2 h-1.5" value={expiry.expired ? 0 : Math.min(30, expiry.days)} max={30}
                tone={expiryTone} barClassName={expiryTone ? undefined : 'bg-brand'}
              />
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
