import { useMemo, useState } from 'react'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestGroupReportItem } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { HelpTip } from '@/components/common/HelpTip'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { DataTable } from '@/components/data/DataTable'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { IconWarning } from '@/lib/icons'
import { useDestGroupReport, useDestGroups, useDestLists, useDestSettings, useDestStatus, useEditDestListEntries } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { allowlistSearch, parseAllowlistParams, type SettingsFocus } from '../accessParams'
import { minutesWords } from '../confirmCopy'
import { reportDestError } from '../destErrors'
import { reportDayOptions, stateDay } from './allowlistState'

const R = 'admin:access_control.allowlist.report.'
const IP = '(ip)'
const HOUR = 3_600_000
const DAY = 24 * HOUR

export interface AllowlistReportProps {
  groupId: number
  /** Leaves the report: Back when the report was pushed, the cards otherwise. */
  onBack: () => void
  onOpenEntries: (listId: number) => void
  onTestDestination: (target: string) => void
  onOpenSettings: (focus?: SettingsFocus) => void
  onTurnOn: () => void
  onViewDenied: () => void
}

/**
 * WHAT A REPORT-ONLY ALLOWLIST WOULD HAVE DENIED (S10): the sites the group's
 * members reached that are not in its lists, over a range the trial retention
 * limits. The admin adds the ones that are wanted, one by one or together, to
 * the group's supplement list (one entries request each time, never a rewrite
 * of the list), and turns the allowlist on when the rest is acceptable. Only
 * the registrable domain is recorded: no full host names and no accounts.
 */
export default function AllowlistReport({ groupId, onBack, onOpenEntries, onTestDestination, onOpenSettings, onTurnOn, onViewDenied }: AllowlistReportProps) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const [params, setParams] = useSearchParams()
  const groups = useDestGroups(scope)
  const settings = useDestSettings(scope)
  const status = useDestStatus(scope)
  const lists = useDestLists(scope)
  const edit = useEditDestListEntries(scope)
  const num = useMemo(() => new Intl.NumberFormat(i18n.language), [i18n.language])

  const group = groups.data?.find(g => g.group_id === groupId)
  const retention = settings.data?.effective.dest_trial_retention_days
  const options = reportDayOptions(retention)
  const asked = parseAllowlistParams(params).days ?? Math.min(7, retention && retention > 0 ? retention : 7)
  const days = options.includes(asked) ? asked : options[options.length - 1]
  const read = useDestGroupReport(scope, groupId, days)
  const data = read.data

  const [sorting, setSorting] = useState<SortingState>([{ id: 'count', desc: true }])
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [added, setAdded] = useState<Set<string>>(new Set())
  const [target, setTarget] = useState<number | null>(null)

  const extraId = group?.extra_list_id ?? 0
  const targetId = target ?? extraId
  // The server names a group's own lists "<group> · <list>"; until the lists are read the supplement is named the same way here.
  const listName = (id: number) => lists.data?.items.find(l => l.id === id)?.name
    ?? (id === extraId ? t(`${R}extra_name`, { group: group?.group_name ?? '' }) : String(id))
  const targets = [extraId, ...(group?.lists ?? []).map(l => l.id).filter(id => lists.data?.items.find(l => l.id === id)?.kind === 'custom')].filter(id => id > 0)

  const isAdded = (i: DestGroupReportItem) => i.added || added.has(i.site)
  const rows = useMemo(() => data?.items ?? [], [data])
  const max = rows.reduce((m, r) => Math.max(m, r.count), 0)
  const total = rows.reduce((n, r) => n + r.count, 0)
  const unhandled = rows.filter(r => r.site !== IP && !isAdded(r)).length
  const addable = (i: DestGroupReportItem) => i.site !== IP && !isAdded(i)

  const setDays = (d: number) => setParams(allowlistSearch(params, { days: d }), { replace: true })

  /** One entries request per call, never a rewrite of the list: the sites go in as `domain:<site>` additions. */
  const add = async (sites: string[]) => {
    if (sites.length === 0 || targetId <= 0) return
    try {
      await edit.mutateAsync({ id: targetId, add: sites.map(s => `domain:${s}`) })
      // "Added" means in the group's own lists, which is what the server marks; another list the admin picked is not.
      if (targetId === extraId) setAdded(prev => new Set([...prev, ...sites]))
      setSelected({})
      pushSnack(t(`${R}snack_added`, { count: sites.length, list: listName(targetId), minutes: minutesWords(t, applyMinutes(status.data)) }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  const on = group?.state === 'on'
  const since = data?.since_at ?? null
  const sinceMs = since === null ? null : Date.now() - since
  // The day is counted as the card's badge counts it (the day it began is day 1), by the one rule in stateDay.
  const age = since === null || sinceMs === null ? null : sinceMs < DAY ? { young: true, n: Math.max(1, Math.floor(sinceMs / HOUR)) } : { young: false, n: stateDay(since, Date.now()) ?? 1 }
  const noneEver = data !== undefined && since === null && data.items.length === 0

  const columns = useMemo<ColumnDef<DestGroupReportItem, any>[]>(() => [
    {
      id: 'site', header: t(`${R}col_site`), enableSorting: false,
      meta: { hideable: false, label: t(`${R}col_site`), className: 'min-w-0 [overflow-wrap:anywhere]' },
      cell: ctx => {
        const r = ctx.row.original
        const done = r.added || added.has(r.site)
        return (
          <div className="min-w-0">
            {r.site === IP
              ? <span className="font-mono">{IP}</span>
              : (
                <button type="button" onClick={() => onTestDestination(r.site)}
                  className="rounded-sm text-left font-mono underline-offset-2 hover:underline focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none">{r.site}</button>
              )}
            {done && <p className="text-sm text-muted-foreground">{t(`${R}added`)}</p>}
            {r.site === IP && <p className="text-sm whitespace-normal text-muted-foreground">{t(`${R}ip_note`)}</p>}
          </div>
        )
      },
    },
    {
      id: 'count', header: t(`${R}col_count`), accessorFn: r => r.count,
      meta: { label: t(`${R}col_count`), className: 'w-44' },
      cell: ctx => <CountBar value={ctx.row.original.count} max={max} formatter={num} />,
    },
    {
      id: 'last', header: t(`${R}col_last`), accessorFn: r => r.last_at,
      meta: { label: t(`${R}col_last`), className: 'w-28 text-muted-foreground' },
      cell: ctx => formatRelativeTimeShort(Date.now() - ctx.row.original.last_at, t),
    },
    {
      id: 'actions', header: '', enableSorting: false,
      meta: { hideable: false, align: 'right', label: t(`${R}add`), className: 'w-36' },
      cell: ctx => {
        const r = ctx.row.original
        if (r.site === IP) return <Button size="sm" variant="outline" onClick={() => onOpenEntries(extraId)} disabled={extraId <= 0}>{t(`${R}edit_extra`)}</Button>
        if (r.added || added.has(r.site)) return null
        return <AsyncButton size="sm" variant="outline" onClick={() => add([r.site])}>{t(`${R}add`)}</AsyncButton>
      },
    },
    // The row handlers close over this render's list and target; the columns are rebuilt when those change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [t, max, num, added, extraId, targetId, onTestDestination, onOpenEntries])

  const selectedSites = Object.keys(selected).filter(k => selected[k])

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <nav aria-label={t(`${R}crumb_label`)} className="min-w-0 flex-1">
          <ol className="flex flex-wrap items-center gap-1.5 text-sm">
            <li><button type="button" onClick={onBack} className="rounded-sm underline-offset-2 hover:underline focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none">{t(`${R}crumb_root`)}</button></li>
            <li aria-hidden className="text-muted-foreground">/</li>
            <li className="[overflow-wrap:anywhere]">{group?.group_name ?? '…'}</li>
            <li aria-hidden className="text-muted-foreground">/</li>
            <li><h2 className="text-sm font-semibold">{on ? t(`${R}title_on`, { group: group?.group_name ?? '' }) : t(`${R}crumb_report`)}</h2></li>
          </ol>
        </nav>
        {on
          ? <Button size="sm" variant="outline" onClick={onViewDenied}>{t(`${R}view_denied`)}</Button>
          : group?.state === 'report' && (noneEver || rows.length > 0) && <Button size="sm" onClick={onTurnOn}>{t(`${R}turn_on`)}</Button>}
      </div>
      {on && <p className="mb-2 text-sm text-muted-foreground">{t(`${R}on_note`)}</p>}

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="text-sm text-muted-foreground">{t(`${R}range`)}</span>
        <SegmentedControl size="sm" value={String(days)} aria-label={t(`${R}range`)} onChange={v => setDays(Number(v))}
          options={options.map(d => ({ value: String(d), label: t(`${R}day`, { n: d }) }))} />
        <span className="text-sm text-muted-foreground">
          {t(`${R}range_note`, { days: options[options.length - 1] })}{' '}
          <button type="button" onClick={() => onOpenSettings('trial')} className="rounded-sm underline underline-offset-2 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none">{t(`${R}edit_retention`)}</button>
        </span>
      </div>
      {data && data.effective_days < days && <p className="mt-1 text-sm text-muted-foreground">{t(`${R}clamped`, { days: data.effective_days })}</p>}

      {read.isError && <ErrorState className="mt-3" title={t(`${R}read_failed`)} reason={read.error} retry={() => read.refetch()} />}

      {(read.isPending || data) && (
        <>
          {data && (
            <KpiGrid>
              <KpiTile label={t(`${R}kpi_sites`)} value={num.format(rows.length)} />
              <KpiTile label={t(`${R}kpi_count`)} value={num.format(total)} />
              <KpiTile label={t(`${R}kpi_open`)} value={num.format(unhandled)} />
              <KpiTile label={t(`${R}kpi_lost`)} value={num.format(data.dropped_in_range)} />
            </KpiGrid>
          )}
          {data && data.dropped_in_range > 0 && (
            <p className="mb-2 flex items-start gap-1.5 text-sm">
              <IconWarning aria-hidden className="mt-0.5 size-4 shrink-0 text-warning-ink" />
              {t(`${R}lost_note`)}
            </p>
          )}
          {age && <p className="mb-2 text-sm text-muted-foreground">{age.young ? t(`${R}age_young`, { hours: age.n }) : t(`${R}age`, { days: age.n })}</p>}

          {noneEver && <div className="rounded-lg bg-muted p-4 text-sm">{t(`${R}never`)}</div>}
          {data && !noneEver && rows.length === 0 && (
            <div className="flex flex-wrap items-center gap-2 rounded-lg bg-muted p-4 text-sm">
              <p className="flex-1">{t(`${R}empty`, { days: data.effective_days })}</p>
              {group?.state === 'report' && <Button size="sm" variant="outline" onClick={onTurnOn}>{t(`${R}turn_on`)}</Button>}
            </div>
          )}
          {(read.isPending || rows.length > 0) && (
            <>
              <p className="mb-1 flex items-center gap-1 text-xs text-muted-foreground">
                {t(`${R}col_site`)}
                <HelpTip text={t(`${R}site_help`)} label={t(`${R}site_help_label`)} />
              </p>
              <DataTable<DestGroupReportItem>
                aria-label={t(`${R}crumb_report`)}
                data={rows}
                columns={columns}
                getRowId={r => r.site}
                loading={read.isPending}
                pagination={false}
                sorting={sorting}
                onSortingChange={setSorting}
                selectable
                selection={selected}
                onSelectionChange={setSelected}
                rowLabel={r => r.site}
                // The (ip) row and a site already in the list cannot be added by name, so they are not ticked.
                rowOptions={r => ({ selectable: addable(r), state: isAdded(r) ? 'dimmed' : undefined })}
                columnMenu={false}
                bulkActions={() => (
                  <>
                    {targets.length > 1 && (
                      <NativeSelect aria-label={t(`${R}add_to`)} value={targetId} onChange={e => setTarget(Number(e.target.value))} className="w-44">
                        {targets.map(id => <option key={id} value={id}>{listName(id)}</option>)}
                      </NativeSelect>
                    )}
                    <AsyncButton onClick={() => add(selectedSites)}>{t(`${R}add_n`, { n: selectedSites.length })}</AsyncButton>
                  </>
                )}
              />
            </>
          )}
        </>
      )}
    </div>
  )
}

/** A count with a bar behind it, relative to the largest count on the page: a hint, the number is always written. */
function CountBar({ value, max, formatter }: { value: number; max: number; formatter: Intl.NumberFormat }) {
  const pct = !(max > 0) || !(value > 0) ? 0 : Math.min(100, Math.max(2, Math.round((value / max) * 100)))
  return (
    <div className="flex min-w-0 items-center gap-2">
      <div aria-hidden className="h-2 min-w-10 flex-1 overflow-hidden rounded bg-primary/10">
        <div data-testid="bar-fill" className="h-full bg-primary/60" style={{ width: `${pct}%` }} />
      </div>
      <span className="min-w-14 text-right tabular-nums">{formatter.format(value)}</span>
    </div>
  )
}
