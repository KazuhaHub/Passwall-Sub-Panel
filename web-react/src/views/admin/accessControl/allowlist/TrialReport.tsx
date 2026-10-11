import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Checkbox, MenuItem, Paper, Skeleton, Stack, TablePagination, TextField, ToggleButton, ToggleButtonGroup, Tooltip, Typography, useTheme } from '@mui/material'
import { AsyncButton } from '@/components/AsyncButton'
import BarCount from '@/components/BarCount'
import FieldHint from '@/components/FieldHint'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { pushSnack } from '@/components/SnackbarHost'
import { useAddDestinationGroupSites, useDestinationGroup, useDestinationGroupTrial } from '@/query/destinationGroups'
import { useDestinationLists } from '@/query/accessControl'
import type { QueryScope } from '@/query/session'
import PendingActionGuard from '../PendingActionGuard'
import { useAccessTranslation } from '../useAccessTranslation'
import { formatRelativeTimeShort } from '@/utils/relativeTime'

const P = 'admin:access_control.trial_report.'
export default function TrialReport({ scope, groupID, days, retentionDays, onDays, onBack, onEnforce, onList, onTest, onDenied }: {
  scope: QueryScope; groupID: number; days: number; retentionDays?: number
  onDays: (days: number) => void; onBack: () => void; onEnforce: (groupID: number) => void
  onList: (listID: number) => void; onTest: (site: string, groupID: number) => void; onDenied: (groupID: number) => void
}) {
  const { t, number, dateTime } = useAccessTranslation(['admin', 'common']), theme = useTheme()
  const detail = useDestinationGroup(scope, groupID), lists = useDestinationLists(scope)
  const ranges = [1, 3, 7].filter(value => value <= (retentionDays ?? 7))
  const requested = ranges.includes(days) ? days : ranges.at(-1) ?? 1
  useEffect(() => { if (requested !== days) onDays(requested) }, [requested, days, onDays])
  const group = !detail.error ? detail.data : undefined
  const trialReadable = !!group && detail.isFetchedAfterMount && group.stage !== 'enforce'
  const report = useDestinationGroupTrial(scope, groupID, requested, trialReadable), add = useAddDestinationGroupSites(scope)
  const [search, setSearch] = useState(''), [sort, setSort] = useState<{ field: 'count' | 'last_at'; desc: boolean }>({ field: 'count', desc: true })
  const [selected, setSelected] = useState<string[]>([]), [target, setTarget] = useState<number | null>(null), [page, setPage] = useState(0), [pageSize, setPageSize] = useState(50)
  const [submitting, setSubmitting] = useState(false), [failed, setFailed] = useState(false), admission = useRef(false)
  const busy = submitting || add.isPending, data = report.error ? undefined : report.data
  const mayWrite = !!group && group.mode === 'allowlist' && group.stage === 'trial' && !detail.error && !lists.error && !!lists.data
  const allowedIDs = new Set([...(group?.list_ids ?? []), group?.extra_list_id])
  const targets = lists.error ? [] : lists.data?.items.filter(list => list.kind === 'custom' && allowedIDs.has(list.id) && list.id !== group?.base_list_id && (list.owner_group_id === 0 || list.owner_group_id === group?.group_id)) ?? []
  const targetID = target !== null && targets.some(list => list.id === target) ? target : group?.extra_list_id ?? 0
  const usable = data?.items.filter(site => !site.added && site.site !== '(ip)') ?? []
  const activeSelected = selected.filter(site => usable.some(item => item.site === site))
  const write = async (sites: string[], listID: number) => {
    if (admission.current || busy || !mayWrite || !targets.some(list => list.id === listID) || sites.length === 0 || !sites.every(site => usable.some(item => item.site === site))) return
    admission.current = true; setSubmitting(true); setFailed(false)
    try {
      await add.mutateAsync({ listID, sites })
      setSelected(current => current.filter(site => !sites.includes(site)))
      pushSnack(t(`${P}saved`, { count: sites.length, name: targets.find(list => list.id === listID)?.name }), 'success')
    } catch { setFailed(true); pushSnack(t(`${P}add_failed`), 'error') }
    finally { admission.current = false; setSubmitting(false) }
  }
  const changeSort = (field: 'count' | 'last_at') => { setSort(current => ({ field, desc: current.field === field ? !current.desc : true })); setPage(0) }
  const all = data?.items ?? [], totalCount = all.reduce((total, site) => Math.min(9223372036854775807, total + site.count), 0)
  const filtered = all.filter(site => site.site.toLocaleLowerCase().includes(search.toLocaleLowerCase())).sort((a, b) => {
    const delta = a[sort.field] - b[sort.field]
    return delta ? sort.desc ? -delta : delta : a.site < b.site ? -1 : a.site > b.site ? 1 : 0
  })
  const safePage = Math.min(page, Math.max(0, Math.ceil(filtered.length / pageSize) - 1))
  const visible = filtered.slice(safePage * pageSize, (safePage + 1) * pageSize)
  const max = all.reduce((value, site) => Math.max(value, site.count), 0)
  const ageHours = group?.stage_changed_at == null ? null : (Date.now() - group.stage_changed_at) / 3600000
  return <Stack spacing={2} sx={{ minWidth: 0 }}>
    <PendingActionGuard hold={busy} />
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ justifyContent: 'space-between', alignItems: { xs: 'stretch', sm: 'center' } }}>
      <Button disabled={busy} onClick={onBack} sx={{ minHeight: 44, alignSelf: 'flex-start' }}>{t(`${P}back`)}</Button>
      <Typography component="h2" variant="h6" sx={{ flex: 1, overflowWrap: 'anywhere' }}>{t(group?.stage === 'enforce' ? `${P}denied_title` : `${P}title`, { name: group?.group_name ?? '' })}</Typography>
      {group?.stage === 'trial' && <Button disabled={busy} variant="outlined" onClick={() => onEnforce(groupID)} sx={{ minHeight: 44 }}>{t('admin:access_control.allowlist.enforce')}</Button>}
    </Stack>
    {detail.error ? <Alert severity="error" action={<AsyncButton onClick={() => detail.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert> : !group ? <Skeleton height={180} role="progressbar" aria-label={t(`${P}loading`)} /> : group.stage === 'enforce' ? <>
      <Typography>{t(`${P}denied_count`, { count: group.denied_24h })}</Typography><Button onClick={() => onDenied(groupID)}>{t(`${P}view_denied`)}</Button>
    </> : <>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'flex-start', sm: 'center' } }}>
        <ToggleButtonGroup exclusive size="small" value={requested} disabled={busy} onChange={(_, value: number | null) => { if (value !== null) onDays(value) }} aria-label={t(`${P}range`)}>{ranges.map(value => <ToggleButton key={value} value={value} sx={{ minHeight: 44 }}>{t(`${P}days`, { days: value })}</ToggleButton>)}</ToggleButtonGroup>
        <Typography variant="body2" color="text.secondary">{t(`${P}retention`, { days: retentionDays ?? data?.effective_days ?? '—' })}</Typography>
        {data && data.effective_days < requested && <Typography variant="body2" color="text.secondary">{t(`${P}effective_days`, { days: data.effective_days })}</Typography>}
      </Stack>
      {group.mode === 'open' && <Alert severity="info">{t(`${P}closed`)}</Alert>}
      {ageHours !== null && ageHours >= 0 && ageHours < 24 && <Typography variant="body2" color="text.secondary">{t(`${P}young`, { hours: Math.floor(ageHours), recommend: Math.min(7, retentionDays ?? data?.effective_days ?? 7) })}</Typography>}
      {report.error ? <Alert severity="error" action={<AsyncButton pending={report.isFetching} onClick={() => report.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert> : !data || !trialReadable ? <Stack spacing={1} aria-busy="true">{Array.from({ length: 8 }, (_, key) => <Skeleton key={key} height={56} variant="rounded" />)}</Stack> : <>
        <KpiGrid><KpiTile label={t(`${P}sites`)} value={number(all.length)} /><KpiTile label={t(`${P}count`)} value={number(totalCount)} /><KpiTile label={t(`${P}unhandled`)} value={number(all.filter(site => !site.added).length)} /><KpiTile label={t(`${P}completeness`)} value={t(`${P}incomplete`)} color={data.losses.rows || data.losses.events || data.losses.unmatched ? theme.palette.warning.main : undefined} /></KpiGrid>
        <FieldHint tone="muted" summary={t(`${P}loss_details`)} detail={t(`${P}loss_scope`, { rows: data.losses.rows, events: data.losses.events, unmatched: data.losses.unmatched })} />
        <Typography variant="body2" color="text.secondary">{t(`${P}privacy`)}</Typography>
        {lists.error && <Alert severity="warning" action={<AsyncButton pending={lists.isFetching} onClick={() => lists.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}lists_failed`)}</Alert>}
        {failed && <Alert severity="error">{t(`${P}add_failed`)}</Alert>}
        <TextField size="small" label={t(`${P}search`)} value={search} onChange={event => { setSearch(event.target.value); setPage(0) }} />
        <Stack direction="row" spacing={1}><Button aria-pressed={sort.field === 'count'} onClick={() => changeSort('count')}>{t(`${P}sort_count`)}</Button><Button aria-pressed={sort.field === 'last_at'} onClick={() => changeSort('last_at')}>{t(`${P}sort_recent`)}</Button></Stack>
        {visible.length === 0 ? <Stack spacing={1}><Typography>{t(search ? `${P}no_matches` : `${P}empty`, { days: data.effective_days })}</Typography>{search && <Button onClick={() => setSearch('')}>{t(`${P}clear_search`)}</Button>}</Stack> : visible.map(site => <Paper key={site.site} component="article" aria-label={site.site} variant="outlined" sx={{ p: 2, borderRadius: 2, opacity: site.added ? .6 : 1, minWidth: 0 }}>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5} sx={{ alignItems: { xs: 'stretch', sm: 'center' } }}>
            {!site.added && site.site !== '(ip)' && <Checkbox slotProps={{ input: { 'aria-label': site.site } }} disabled={busy || !mayWrite} checked={activeSelected.includes(site.site)} onChange={event => setSelected(current => event.target.checked ? [...current, site.site] : current.filter(value => value !== site.site))} />}
            {site.site === '(ip)' ? <Typography>{site.site}</Typography> : <Button disabled={busy} onClick={() => onTest(site.site, groupID)} sx={{ minHeight: 44, textTransform: 'none', overflowWrap: 'anywhere' }}>{site.site}</Button>}
            <Box sx={{ flex: 1 }}><BarCount count={site.count} max={max} /><Tooltip title={dateTime(site.last_at)}><Typography variant="caption" color="text.secondary">{formatRelativeTimeShort(Date.now() - site.last_at, t)}</Typography></Tooltip></Box>
            {site.added ? <Typography variant="body2">{t(`${P}added`)}</Typography> : site.site === '(ip)' ? <Button disabled={busy || !group.extra_list_id} onClick={() => onList(group.extra_list_id)} sx={{ minHeight: 44 }}>{t(`${P}edit_extra`)}</Button> : <AsyncButton pending={busy} disabled={!mayWrite || !targets.some(list => list.id === group.extra_list_id)} onClick={() => write([site.site], group.extra_list_id)}>{t(`${P}add`)}</AsyncButton>}
          </Stack>
          {site.site === '(ip)' && <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{t(`${P}ip_note`)}</Typography>}
        </Paper>)}
        {filtered.length > 50 && <TablePagination component="div" count={filtered.length} page={safePage} rowsPerPage={pageSize} rowsPerPageOptions={[50, 100, 200]} onPageChange={(_, value) => setPage(value)} onRowsPerPageChange={event => { setPageSize(Number(event.target.value)); setPage(0) }} labelRowsPerPage={t(`${P}per_page`)} labelDisplayedRows={({ from, to, count }) => t(`${P}page_rows`, { from, to, count })} />}
        {activeSelected.length > 0 && <Paper variant="outlined" sx={{ position: 'sticky', bottom: 8, zIndex: 1, p: 2, borderRadius: 3, bgcolor: theme.palette.md.surfaceContainerHigh }}><Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5} sx={{ alignItems: { xs: 'stretch', sm: 'center' } }}>
          <Typography>{t(`${P}selected`, { count: activeSelected.length })}</Typography>
          <TextField select size="small" label={t(`${P}add_to`)} value={targets.some(list => list.id === targetID) ? targetID : ''} disabled={busy || !mayWrite} sx={{ flex: 1, minWidth: 160 }} onChange={event => setTarget(Number(event.target.value))}>{targets.map(list => <MenuItem key={list.id} value={list.id}>{list.name}</MenuItem>)}</TextField>
          <AsyncButton variant="contained" pending={busy} disabled={!mayWrite || !targets.some(list => list.id === targetID)} onClick={() => write(activeSelected, targetID)}>{t(`${P}add_selected`, { count: activeSelected.length })}</AsyncButton>
        </Stack></Paper>}
      </>}
    </>}
  </Stack>
}
