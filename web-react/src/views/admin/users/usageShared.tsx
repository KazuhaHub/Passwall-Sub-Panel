import type { ReactNode } from 'react'
import type { TFunction } from 'i18next'

import { IconArrowDown, IconArrowUp } from '@/lib/icons'
import { TableHead } from '@/components/ui/table'
import { cn } from '@/lib/utils'

/** A byte count, compactly (single-letter unit). */
export function fmt(n: number): string {
  if (!n) return '0'
  const units = ['B', 'K', 'M', 'G', 'T']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v >= 100 || u === 0 ? Math.round(v) : v.toFixed(1)}${units[u]}`
}

export type SortKey = 'lifetime' | 'period' | 'today'
export type SortDir = 'asc' | 'desc'

/** The byte fields the per-node and per-server rows share; the grand total sums exactly these. */
export interface UsageFields {
  lifetime_total_bytes: number; lifetime_up_bytes: number; lifetime_down_bytes: number
  period_total_bytes: number; period_up_bytes: number; period_down_bytes: number
  today_total_bytes: number; today_up_bytes: number; today_down_bytes: number
  charged_lifetime_bytes: number; charged_period_bytes: number
}

export interface UsageTotals {
  lifetime: number; lifeUp: number; lifeDown: number
  period: number; periodUp: number; periodDown: number
  chargedLifetime: number; chargedPeriod: number
  today: number; todayUp: number; todayDown: number
}

export function sumUsage(rows: readonly UsageFields[]): UsageTotals {
  return rows.reduce<UsageTotals>((a, r) => ({
    lifetime: a.lifetime + r.lifetime_total_bytes, lifeUp: a.lifeUp + r.lifetime_up_bytes, lifeDown: a.lifeDown + r.lifetime_down_bytes,
    period: a.period + r.period_total_bytes, periodUp: a.periodUp + r.period_up_bytes, periodDown: a.periodDown + r.period_down_bytes,
    chargedLifetime: a.chargedLifetime + (r.charged_lifetime_bytes ?? 0), chargedPeriod: a.chargedPeriod + (r.charged_period_bytes ?? 0),
    today: a.today + r.today_total_bytes, todayUp: a.todayUp + r.today_up_bytes, todayDown: a.todayDown + r.today_down_bytes,
  }), { lifetime: 0, lifeUp: 0, lifeDown: 0, period: 0, periodUp: 0, periodDown: 0, chargedLifetime: 0, chargedPeriod: 0, today: 0, todayUp: 0, todayDown: 0 })
}

/** The figure the sort key orders by, in the shown basis. */
export function usageMetric(r: UsageFields, key: SortKey, charged: boolean): number {
  return key === 'lifetime' ? (charged ? r.charged_lifetime_bytes : r.lifetime_total_bytes)
    : key === 'today' ? r.today_total_bytes
      : (charged ? r.charged_period_bytes : r.period_total_bytes)
}

/** One metric cell: the total over a small ↑up ↓down line, right-aligned, digits of equal width. */
export function RawCell({ total, up, down }: { total: number; up: number; down: number }) {
  return (
    <div className="text-right tabular-nums">
      <div className="font-medium">{fmt(total)}</div>
      <div className="text-xs whitespace-nowrap text-muted-foreground">↑{fmt(up)} ↓{fmt(down)}</div>
    </div>
  )
}

/** Charged mode: the charged total over the raw amount (the charged figure has no up/down split). */
export function ChargedCell({ charged, raw, t }: { charged: number; raw: number; t: TFunction }) {
  return (
    <div className="text-right tabular-nums">
      <div className="font-medium">{fmt(charged)}</div>
      <div className="text-xs whitespace-nowrap text-muted-foreground">{t('users.nodeusage.raw_amount', { amount: fmt(raw) })}</div>
    </div>
  )
}

/** A sortable column header: a real button inside the th, with aria-sort on the th. */
export function SortHead({ active, dir, onSort, children }: {
  active: boolean; dir: SortDir; onSort: () => void; children: ReactNode
}) {
  const Arrow = dir === 'asc' ? IconArrowUp : IconArrowDown
  return (
    <TableHead className="text-right" aria-sort={active ? (dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" onClick={onSort}
        className={cn('inline-flex items-center gap-1 rounded-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50', !active && 'text-muted-foreground')}>
        {children}
        <Arrow aria-hidden className={cn('size-3.5', !active && 'opacity-0')} />
      </button>
    </TableHead>
  )
}
