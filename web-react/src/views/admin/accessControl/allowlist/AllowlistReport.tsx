import { useMemo, useState } from 'react'
import {
  Alert, Box, Breadcrumbs, Button, Checkbox, Link, MenuItem, Skeleton, TextField, ToggleButton, ToggleButtonGroup, Typography, useTheme,
} from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestGroupReportItem } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import BarCount from '@/components/BarCount'
import BulkBar from '@/components/BulkBar'
import HelpTip from '@/components/HelpTip'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { amber } from '@/components/ToneBadge'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestGroupReport, useDestGroups, useDestLists, useDestSettings, useDestStatus, useEditDestListEntries } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { allowlistSearch, parseAllowlistParams, type SettingsFocus } from '../accessParams'
import { minutesWords } from '../confirmCopy'
import { reportDestError } from '../destErrors'
import { reportDayOptions } from './allowlistState'

const R = 'admin:access_control.allowlist.report.'
const IP = '(ip)'
const HOUR = 3_600_000
const DAY = 24 * HOUR

type SortKey = 'count' | 'last'

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
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const [params, setParams] = useSearchParams()
  const groups = useDestGroups(scope)
  const settings = useDestSettings(scope)
  const status = useDestStatus(scope)
  const lists = useDestLists(scope)
  const edit = useEditDestListEntries(scope)
  const num = new Intl.NumberFormat(i18n.language)

  const group = groups.data?.find(g => g.group_id === groupId)
  const retention = settings.data?.effective.dest_trial_retention_days
  const options = reportDayOptions(retention)
  const asked = parseAllowlistParams(params).days ?? Math.min(7, retention && retention > 0 ? retention : 7)
  const days = options.includes(asked) ? asked : options[options.length - 1]
  const read = useDestGroupReport(scope, groupId, days)
  const data = read.data

  const [sort, setSort] = useState<{ key: SortKey; dir: 'asc' | 'desc' }>({ key: 'count', dir: 'desc' })
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [added, setAdded] = useState<Set<string>>(new Set())
  const [target, setTarget] = useState<number | null>(null)

  const extraId = group?.extra_list_id ?? 0
  const targetId = target ?? extraId
  // The server names a group's own lists "<group> · <list>"; until the lists are read the supplement is named the same way here.
  const listName = (id: number) => lists.data?.items.find(l => l.id === id)?.name
    ?? (id === extraId ? t(`${R}extra_name`, { group: group?.group_name ?? '' }) : String(id))
  const targets = [extraId, ...(group?.lists ?? []).map(l => l.id).filter(id => lists.data?.items.find(l => l.id === id)?.kind === 'custom')].filter(id => id > 0)

  const isAdded = (i: DestGroupReportItem) => i.added || added.has(i.site)
  const rows = useMemo(() => {
    const sign = sort.dir === 'asc' ? 1 : -1
    return [...(data?.items ?? [])].sort((a, b) => sign * (sort.key === 'count' ? a.count - b.count : a.last_at - b.last_at))
  }, [data, sort])
  const max = rows.reduce((m, r) => Math.max(m, r.count), 0)
  const total = rows.reduce((n, r) => n + r.count, 0)
  const unhandled = rows.filter(r => !isAdded(r)).length
  const addable = (i: DestGroupReportItem) => i.site !== IP && !isAdded(i)

  const setDays = (d: number) => setParams(allowlistSearch(params, { days: d }), { replace: true })
  const onSort = (key: SortKey) => setSort(s => ({ key, dir: s.key === key && s.dir === 'desc' ? 'asc' : 'desc' }))
  const toggle = (site: string, on: boolean) => setSelected(prev => {
    const out = new Set(prev)
    if (on) out.add(site); else out.delete(site)
    return out
  })

  const add = async (sites: string[]) => {
    if (sites.length === 0 || targetId <= 0) return
    try {
      await edit.mutateAsync({ id: targetId, add: sites.map(s => `domain:${s}`) })
      setAdded(prev => new Set([...prev, ...sites]))
      setSelected(new Set())
      pushSnack(t(`${R}snack_added`, { count: sites.length, list: listName(targetId), minutes: minutesWords(t, applyMinutes(status.data)) }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  const on = group?.state === 'on'
  const since = data?.since_at ?? null
  const sinceMs = since === null ? null : Date.now() - since
  const age = sinceMs === null ? null : sinceMs < DAY ? { young: true, n: Math.max(1, Math.floor(sinceMs / HOUR)) } : { young: false, n: Math.floor(sinceMs / DAY) }
  const noneEver = data !== undefined && since === null && data.items.length === 0

  const sortButton = (key: SortKey, label: string) => (
    <Link component="button" type="button" underline="hover" aria-label={label} onClick={() => onSort(key)}
      sx={{ font: 'inherit', color: md.onSurfaceVariant, fontWeight: sort.key === key ? 600 : 400 }}>
      {label}{sort.key === key ? (sort.dir === 'desc' ? ' ↓' : ' ↑') : ''}
    </Link>
  )

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', mb: 1.5 }}>
        <Breadcrumbs aria-label={t(`${R}crumb_label`)} sx={{ flex: 1, minWidth: 0 }}>
          <Link component="button" type="button" underline="hover" onClick={onBack} sx={{ font: 'inherit' }}>{t(`${R}crumb_root`)}</Link>
          <Typography component="span" sx={{ overflowWrap: 'anywhere' }}>{group?.group_name ?? '…'}</Typography>
          <Typography component="h2" sx={{ fontSize: 'inherit', fontWeight: 600 }}>{on ? t(`${R}title_on`, { group: group?.group_name ?? '' }) : t(`${R}crumb_report`)}</Typography>
        </Breadcrumbs>
        {on
          ? <Button size="small" onClick={onViewDenied}>{t(`${R}view_denied`)}</Button>
          : group?.state === 'report' && (noneEver || rows.length > 0) && <Button size="small" variant="contained" onClick={onTurnOn}>{t(`${R}turn_on`)}</Button>}
      </Box>
      {on && <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1 }}>{t(`${R}on_note`)}</Typography>}

      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap' }}>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${R}range`)}</Typography>
        <ToggleButtonGroup exclusive size="small" value={days} aria-label={t(`${R}range`)} onChange={(_, v: number | null) => { if (v) setDays(v) }}>
          {options.map(d => <ToggleButton key={d} value={d} sx={{ minHeight: 36 }}>{t(`${R}day`, { n: d })}</ToggleButton>)}
        </ToggleButtonGroup>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
          {t(`${R}range_note`, { days: options[options.length - 1] })}{' '}
          <Link component="button" type="button" underline="always" onClick={() => onOpenSettings('trial')} sx={{ font: 'inherit', verticalAlign: 'baseline' }}>{t(`${R}edit_retention`)}</Link>
        </Typography>
      </Box>
      {data && data.effective_days < days && <Typography variant="body2" sx={{ mt: 0.5, color: md.onSurfaceVariant }}>{t(`${R}clamped`, { days: data.effective_days })}</Typography>}

      {read.isPending && (
        <Box sx={{ mt: 1.5 }}>
          <Skeleton variant="rounded" height={72} sx={{ mb: 1.5 }} />
          {[0, 1, 2, 3, 4, 5, 6, 7].map(i => <Skeleton key={i} variant="rounded" height={40} sx={{ mb: 0.75 }} />)}
        </Box>
      )}
      {read.isError && (
        <Alert severity="error" sx={{ mt: 1.5 }} action={<Button color="inherit" size="small" onClick={() => void read.refetch()}>{t('admin:access_control.retry')}</Button>}>
          {t(`${R}read_failed`)}
        </Alert>
      )}

      {data && (
        <>
          <KpiGrid>
            <KpiTile label={t(`${R}kpi_sites`)} value={num.format(rows.length)} />
            <KpiTile label={t(`${R}kpi_count`)} value={num.format(total)} />
            <KpiTile label={t(`${R}kpi_open`)} value={num.format(unhandled)} />
            <KpiTile label={t(`${R}kpi_lost`)} value={num.format(data.dropped_in_range)} />
          </KpiGrid>
          {data.dropped_in_range > 0 && (
            <Typography variant="body2" sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5, mb: 1 }}>
              <WarningAmberIcon aria-hidden sx={{ fontSize: 16, mt: '2px', color: amber(theme).fg }} />
              {t(`${R}lost_note`)}
            </Typography>
          )}
          {age && <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1 }}>{age.young ? t(`${R}age_young`, { hours: age.n }) : t(`${R}age`, { days: age.n })}</Typography>}

          {noneEver && (
            <Box sx={{ p: 2, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}><Typography variant="body2">{t(`${R}never`)}</Typography></Box>
          )}
          {!noneEver && rows.length === 0 && (
            <Box sx={{ p: 2, borderRadius: 2, bgcolor: md.surfaceContainerHigh, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
              <Typography variant="body2" sx={{ flex: 1 }}>{t(`${R}empty`, { days: data.effective_days })}</Typography>
              {group?.state === 'report' && <Button size="small" onClick={onTurnOn}>{t(`${R}turn_on`)}</Button>}
            </Box>
          )}

          {rows.length > 0 && (
            <Box sx={{ borderRadius: 3, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surfaceContainerLow, px: { xs: 1.5, sm: 2 } }}>
              <Box sx={{ display: { xs: 'none', sm: 'grid' }, gridTemplateColumns: ROW_COLUMNS, gap: 1.5, alignItems: 'center', py: 1, fontSize: 12 }}>
                <span />
                <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant, display: 'inline-flex', alignItems: 'center' }}>
                  {t(`${R}col_site`)}
                  <HelpTip textKey="admin:access_control.allowlist.report.site_help" labelKey="admin:access_control.allowlist.report.site_help_label" />
                </Typography>
                {sortButton('count', t(`${R}col_count`))}
                {sortButton('last', t(`${R}col_last`))}
                <span />
              </Box>
              {rows.map((r, i) => {
                const done = isAdded(r)
                const ip = r.site === IP
                return (
                  <Box key={r.site} data-testid="report-row" data-site={r.site} sx={{
                    display: 'grid', gridTemplateColumns: { xs: '36px minmax(0, 1fr)', sm: ROW_COLUMNS }, gap: { xs: 0.5, sm: 1.5 }, alignItems: 'center',
                    py: 1, borderTop: `1px solid ${md.outlineVariant}`, opacity: done ? 0.6 : 1, ...(i === 0 ? { borderTopColor: 'transparent' } : {}),
                  }}>
                    <Box sx={{ width: 36 }}>
                      {addable(r) && <Checkbox size="small" checked={selected.has(r.site)} onChange={(_, c) => toggle(r.site, c)} slotProps={{ input: { 'aria-label': t(`${R}select`, { site: r.site }) } }} />}
                    </Box>
                    <Box sx={{ minWidth: 0 }}>
                      {ip
                        ? <Typography sx={{ fontFamily: 'ui-monospace, monospace' }}>{IP}</Typography>
                        : (
                          <Link component="button" type="button" underline="hover" onClick={() => onTestDestination(r.site)}
                            sx={{ font: 'inherit', fontFamily: 'ui-monospace, monospace', textAlign: 'left', overflowWrap: 'anywhere' }}>{r.site}</Link>
                        )}
                      {done && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${R}added`)}</Typography>}
                    </Box>
                    <Box sx={{ gridColumn: { xs: '2', sm: 'auto' } }}><BarCount value={r.count} max={max} formatter={num} /></Box>
                    <Typography variant="body2" sx={{ gridColumn: { xs: '2', sm: 'auto' }, color: md.onSurfaceVariant }}>{formatRelativeTimeShort(Date.now() - r.last_at, t)}</Typography>
                    <Box sx={{ gridColumn: { xs: '2', sm: 'auto' }, justifySelf: { sm: 'end' } }}>
                      {addable(r) && <AsyncButton size="small" onClick={() => add([r.site])}>{t(`${R}add`)}</AsyncButton>}
                      {ip && <Button size="small" onClick={() => onOpenEntries(extraId)} disabled={extraId <= 0}>{t(`${R}edit_extra`)}</Button>}
                    </Box>
                    {ip && <Typography variant="body2" sx={{ gridColumn: { xs: '1 / -1', sm: '2 / -1' }, color: md.onSurfaceVariant }}>{t(`${R}ip_note`)}</Typography>}
                  </Box>
                )
              })}
            </Box>
          )}
          <BulkBar count={selected.size} label={t(`${R}selected`, { n: selected.size })}>
            {targets.length > 1 && (
              <TextField select size="small" label={t(`${R}add_to`)} value={targetId} onChange={e => setTarget(Number(e.target.value))} sx={{ minWidth: 160 }}>
                {targets.map(id => <MenuItem key={id} value={id}>{listName(id)}</MenuItem>)}
              </TextField>
            )}
            <AsyncButton variant="contained" onClick={() => add([...selected])}>{t(`${R}add_n`, { n: selected.size })}</AsyncButton>
          </BulkBar>
        </>
      )}
    </Box>
  )
}

const ROW_COLUMNS = '36px minmax(0, 1.4fr) minmax(160px, 1fr) 96px 132px'
