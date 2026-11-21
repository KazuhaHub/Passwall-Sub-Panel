import type { TrafficHistoryPeriod } from '@/api/traffic'

/** Ranges the trend can show, in days. */
export const RANGE_DAYS = [1, 7, 30, 90, 180, 365] as const

/** Hour buckets are only readable, and only held raw, for a recent window. */
const HOUR_GRANULARITY_MAX_DAYS = 7

export interface TrendWindow {
  /** The range to query, after clamping to what retention and the period allow. */
  rangeDays: number
  /** The granularity to query, after snapping to what the range supports. */
  period: TrafficHistoryPeriod
  /** The ranges the picker offers for this period. */
  rangeOptions: number[]
}

/**
 * The range and granularity actually requested, from what the URL asks for.
 *
 * Range and period constrain each other: "Today" (1 day) only has hour buckets; a range of 30 days
 * or more has too many hours to read and the raw table does not hold them. Retention
 * (`traffic_history_days`, Infinity for "keep everything") caps the ranges; hour granularity is
 * additionally capped to a week. 1 day is always offered. This used to be a pair of effects that
 * rewrote state after the fact; as a pure function the page never queries a combination it is
 * about to correct, and a shared link with an impossible pair still opens on a valid one.
 */
export function resolveTrendWindow(rangeDays: number, period: TrafficHistoryPeriod, historyDays: number): TrendWindow {
  const snapPeriod = (range: number, p: TrafficHistoryPeriod): TrafficHistoryPeriod =>
    range === 1 ? 'hour' : range >= 30 && p === 'hour' ? 'day' : p
  const optionsFor = (p: TrafficHistoryPeriod): number[] => {
    const cap = p === 'hour' ? Math.min(historyDays, HOUR_GRANULARITY_MAX_DAYS) : historyDays
    return RANGE_DAYS.filter(d => d <= cap || d === 1)
  }
  const first = snapPeriod(rangeDays, period)
  const firstOptions = optionsFor(first)
  // Clamp to the largest offered range that is not larger than the one asked for (or the largest
  // offered, when the request is beyond all of them), as the picker's old effect did.
  const range = firstOptions.includes(rangeDays)
    ? rangeDays
    : firstOptions[firstOptions.length - 1]
  const final = snapPeriod(range, first)
  return { rangeDays: range, period: final, rangeOptions: optionsFor(final) }
}
