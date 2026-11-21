import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { getMyTrafficHistory, type TrafficHistoryItem, type TrafficHistoryPeriod } from '@/api/traffic'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { useMeT } from './useMeT'

const TrafficChart = lazy(() => import('@/components/TrafficChart'))

const RANGES = [1, 7, 30, 90, 180, 365] as const
/** Hourly points over a long window are unreadable, and hour-level detail is only useful for recent inspection. */
const HOUR_GRANULARITY_MAX_DAYS = 7
const CHART_HEIGHT = 280

/** A day as YYYY-MM-DD in the BROWSER's zone. */
const dayString = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

/**
 * The traffic trend chart. Ranges are cut to the admin's retention (a 90-day panel hides "last year"); Day, Week and
 * Month read the hourly roll-up, so they cover the whole retention. "Today" is always there and is hourly only; 30 days
 * or more cannot be hourly. The two controls snap to a valid pair instead of being disabled into a dead end, so the chart
 * never asks for a combination the backend would not answer.
 */
export function TrafficTab({ historyDays }: { historyDays: number | undefined }) {
  const { t } = useMeT()
  const [period, setPeriod] = useState<TrafficHistoryPeriod>('day')
  const [days, setDays] = useState(7)
  const [items, setItems] = useState<TrafficHistoryItem[]>([])
  const [busy, setBusy] = useState(false)

  const rangeOptions = useMemo(() => {
    const retention = historyDays && historyDays > 0 ? historyDays : Number.POSITIVE_INFINITY
    const cap = period === 'hour' ? Math.min(retention, HOUR_GRANULARITY_MAX_DAYS) : retention
    return RANGES.filter(d => d <= cap || d === 1)
  }, [period, historyDays])

  // Clamp: the largest available option that is not bigger than the current selection.
  useEffect(() => {
    if (rangeOptions.length > 0 && !rangeOptions.includes(days as (typeof RANGES)[number])) setDays(rangeOptions[rangeOptions.length - 1])
  }, [rangeOptions, days])
  // Period and range are coupled: 1 day locks to hourly; 30 days or more forbids hourly.
  useEffect(() => {
    if (days === 1 && period !== 'hour') setPeriod('hour')
    else if (days >= 30 && period === 'hour') setPeriod('day')
  }, [days, period])

  // Last wins: a change snaps and reloads, so quick toggling can overlap, and only the latest answer may paint.
  const seq = useRef(0)
  useEffect(() => {
    const mine = ++seq.current
    setBusy(true)
    // The trend is windowed on the viewer's own days (and the api defaults tz to the browser zone), so a person sees
    // their local day boundaries: the same intent as the browser-zone expiry date. Admin views use the panel's zone.
    const since = new Date()
    since.setHours(0, 0, 0, 0)
    since.setDate(since.getDate() - (days - 1))
    getMyTrafficHistory({ period, since: dayString(since), until: dayString(new Date()) })
      .then(res => { if (mine === seq.current) setItems(res.items) })
      .catch(() => { /* the chart stays as it was */ })
      .finally(() => { if (mine === seq.current) setBusy(false) })
  }, [period, days])

  const label: Record<number, string> = {
    1: t('trend.range_1'), 7: t('trend.range_7'), 30: t('trend.range_30'), 90: t('trend.range_90'), 180: t('trend.range_180'), 365: t('trend.range_365'),
  }
  const periods = [
    ...(days <= 7 ? [{ value: 'hour' as const, label: t('trend.period_hour') }] : []),
    { value: 'day' as const, label: t('trend.period_day'), disabled: days === 1 },
    { value: 'week' as const, label: t('trend.period_week'), disabled: days === 1 },
    { value: 'month' as const, label: t('trend.period_month'), disabled: days === 1 },
  ]
  const placeholder = <Skeleton style={{ height: CHART_HEIGHT }} className="w-full" />

  return (
    <Card>
      <CardHeader><CardTitle>{t('trend.title')}</CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          {/* Range first, granularity second: pick the window, then the zoom. */}
          <div className="w-40 max-w-full">
            <NativeSelect aria-label={t('trend.range_label')} value={days} onChange={e => setDays(Number(e.target.value))}>
              {rangeOptions.map(d => <option key={d} value={d}>{label[d]}</option>)}
            </NativeSelect>
          </div>
          <SegmentedControl aria-label={t('trend.period_label')} value={period} onChange={setPeriod} options={periods} />
        </div>
        <Suspense fallback={placeholder}>
          {busy ? placeholder : <TrafficChart items={items} height={CHART_HEIGHT} />}
        </Suspense>
      </CardContent>
    </Card>
  )
}
