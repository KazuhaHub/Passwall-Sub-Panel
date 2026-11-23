import { Fragment, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestHitItem, DestHits } from '@/api/accessControl'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ActionBadge, Dest, R, RuleLink, UserCell, type Namers } from './cells'
import { dayKey, dayRelation, formatClock, formatHourRange } from './hitTime'

// The shell's top bar is sticky over the page, which scrolls as a whole, so the LIST view's day
// headings (the page is their scroller) stick just below it. The table view's headings sit in the
// table's own overflow container, where top: 0 is the right edge and is unchanged.
const STICKY_TOP = 'top-[var(--shell-bar-h,3.5rem)]'

// Widths of the details table's fixed columns (px); the destination takes the rest.
const COLUMN_WIDTH = { hour: 120, user: 170, rule: 230, panel: 110, exit: 120 } as const
const COUNT_WIDTH = 80
const MENU_WIDTH = 52
const DEST_MIN_WIDTH = 200

interface HitListProps extends Namers {
  data: DestHits
  showExit: boolean
  folded: boolean
  tz: string
  now: number
  /** The row menu of one hit: test, add an exception, open the account. */
  menuOf: (hit: DestHitItem) => MoreMenuItem[]
}

/**
 * The details view: one row per account, destination and hour, under a heading per day in the
 * panel's zone. A table on a wide screen, a three-line card on a narrow one (account and count,
 * action and destination, hour and node).
 */
export function HitList({ data, folded, tz, now, num, userName, ruleName, exitLabel, showExit, onOpenUser, onPickSource, menuOf }: HitListProps) {
  const { t, i18n } = useTranslation(['admin'])
  // Consecutive rows of one panel-zone day sit under one heading.
  const groups = useMemo(() => {
    const out: { day: string; ms: number; rows: DestHitItem[] }[] = []
    for (const h of data.items) {
      const day = dayKey(h.hour_ms, tz)
      const last = out.at(-1)
      if (last && last.day === day) last.rows.push(h)
      else out.push({ day, ms: h.hour_ms, rows: [h] })
    }
    return out
  }, [data.items, tz])
  const dayLabel = (ms: number) => {
    const rel = dayRelation(ms, now, tz)
    if (rel !== 'date') return t(`${R}day.${rel}`)
    try {
      return new Intl.DateTimeFormat(i18n.language, { timeZone: tz || undefined, year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(ms))
    } catch {
      return dayKey(ms, tz)
    }
  }
  const hover = (h: DestHitItem) => t(`${R}hover.first_last`, { first: formatClock(h.first_ms, tz), last: formatClock(h.last_ms, tz) })
  const menu = (h: DestHitItem) => <MoreMenu items={menuOf(h)} label={t(`${R}menu.label`)} />

  if (folded) {
    return (
      <div className="grid gap-2">
        {groups.map(g => (
          <Fragment key={g.day + g.ms}>
            <h3 className={`sticky ${STICKY_TOP} z-[1] bg-background py-1 text-[13px] font-semibold text-muted-foreground`}>{dayLabel(g.ms)}</h3>
            {g.rows.map((h, i) => (
              <div key={i} data-testid="hit-row" className="min-w-0 rounded-xl border bg-card p-3">
                <div data-line className="flex min-w-0 items-center gap-2">
                  <div className="min-w-0 flex-1"><span className="sr-only">{t(`${R}cols.user`)}</span><UserCell hit={h} userName={userName} onOpenUser={onOpenUser} /></div>
                  <span className="font-semibold tabular-nums"><span className="sr-only">{t(`${R}cols.count`)}</span>{num.format(h.count)}</span>
                  {menu(h)}
                </div>
                <div data-line className="flex min-w-0 flex-wrap items-center gap-2">
                  <ActionBadge action={h.action} />
                  <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
                  <div className="min-w-0 flex-[1_1_10rem]"><Dest hit={h} /></div>
                </div>
                <p data-line data-testid="hit-time" title={hover(h)} className="mt-1 text-sm text-muted-foreground">
                  {formatHourRange(h.hour_ms, tz)} · {h.panel_name ?? '—'}{showExit && <span data-testid="hit-exit"> → {exitLabel(h)}</span>}
                </p>
              </div>
            ))}
          </Fragment>
        ))}
      </div>
    )
  }

  const columns = showExit ? ['hour', 'user', 'rule', 'dest', 'panel', 'exit'] as const : ['hour', 'user', 'rule', 'dest', 'panel'] as const
  const widthOf = (c: (typeof columns)[number]) => (c === 'dest' ? undefined : COLUMN_WIDTH[c])
  // A fixed layout gives the auto destination column only what the fixed columns leave over, so the
  // table is never narrower than all of them plus room for a destination; a narrower container scrolls.
  const fixedWidth = columns.reduce((sum, c) => sum + (widthOf(c) ?? 0), 0) + COUNT_WIDTH + MENU_WIDTH
  return (
    <Table aria-label={t(`${R}title`)} className="table-fixed" style={{ minWidth: fixedWidth + DEST_MIN_WIDTH }}>
      <TableHeader>
        <TableRow>
          {columns.map(c => <TableHead key={c} style={{ width: widthOf(c) }}>{t(`${R}cols.${c}`)}</TableHead>)}
          <TableHead className="text-right" style={{ width: COUNT_WIDTH }}>{t(`${R}cols.count`)}</TableHead>
          <TableHead style={{ width: MENU_WIDTH }}><span className="sr-only">{t(`${R}menu.label`)}</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {groups.map(g => (
          <Fragment key={g.day + g.ms}>
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={columns.length + 2} className="sticky top-0 bg-background py-1">
                <h3 className="text-[13px] font-semibold text-muted-foreground">{dayLabel(g.ms)}</h3>
              </TableCell>
            </TableRow>
            {g.rows.map((h, i) => (
              <TableRow key={i} data-testid="hit-row">
                <TableCell className="whitespace-nowrap tabular-nums">
                  <span data-testid="hit-time" title={hover(h)}>{formatHourRange(h.hour_ms, tz)}</span>
                </TableCell>
                <TableCell className="whitespace-normal [overflow-wrap:anywhere]"><UserCell hit={h} userName={userName} onOpenUser={onOpenUser} /></TableCell>
                <TableCell className="whitespace-normal">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <ActionBadge action={h.action} />
                    <RuleLink hit={h} ruleName={ruleName} onPickSource={onPickSource} />
                  </div>
                </TableCell>
                <TableCell className="min-w-0"><Dest hit={h} /></TableCell>
                <TableCell className="whitespace-normal [overflow-wrap:anywhere]">{h.panel_name ?? '—'}</TableCell>
                {showExit && <TableCell data-testid="hit-exit" className="whitespace-normal [overflow-wrap:anywhere]">{exitLabel(h)}</TableCell>}
                <TableCell className="text-right tabular-nums">{num.format(h.count)}</TableCell>
                <TableCell className="p-0">{menu(h)}</TableCell>
              </TableRow>
            ))}
          </Fragment>
        ))}
      </TableBody>
    </Table>
  )
}
