import { useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Badge, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, LinearProgress, ListSubheader, Menu, MenuItem, Paper, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme } from '@mui/material'
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import type { DestinationPoliciesView, DestinationStatus } from '@/api/accessControl'
import type { DestinationHitRecord } from '@/api/destinationHits'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useLocation, useSearchParams } from 'react-router'
import { useSiteStore } from '@/stores/site'
import { useAccessTranslation } from '../useAccessTranslation'
import { parseRecordsParams, recordsRequest, recordsSearch, RECORDS_PAGE_SIZES, type RecordsFilters } from './recordsParams'
import { useDestinationHits } from './useDestinationHits'
import { hitTime } from './hitTime'
import HitsMetricCards from './HitsMetricCards'
import HitRow from './HitRow'
import AddExceptionDialog from './AddExceptionDialog'
const P = 'admin:access_control.records.'
const localInput = (ms: number) => { const d = new Date(ms); return new Date(ms - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
const selectProps = { select: { MenuProps: { slotProps: { paper: { sx: { '& .MuiMenuItem-root': { minHeight: 44 } } } } } } }

export default function RecordsTab({ definitions, status, retentionDays, onUser, onTest, onSettings }: {
  definitions?: DestinationPoliciesView; status?: DestinationStatus; retentionDays: number
  onUser: (id: number) => void; onTest: (target: string) => void; onSettings: () => void
}) {
  const { t, number, i18n } = useAccessTranslation(['admin', 'common'])
  const mobile = useMediaQuery(useTheme().breakpoints.down('sm')), timezone = useSiteStore(s => s.timezone)
  const [params, setParams] = useSearchParams(), location = useLocation()
  const parsed = parseRecordsParams(params, retentionDays)
  const [timeDraft, setTimeDraft] = useState<{ since: string; until: string } | null>(null)
  const timeKey = `${parsed.since}/${parsed.until ?? ''}`, lastTimeKey = useRef(timeKey)
  useEffect(() => { if (lastTimeKey.current !== timeKey) { lastTimeKey.current = timeKey; setTimeDraft(null) } }, [timeKey])
  const filters = timeDraft ? { ...parsed, ...timeDraft } : parsed
  const [keyword, setKeyword] = useState(''), [debounced, setDebounced] = useState('')
  const [more, setMore] = useState(false), [menu, setMenu] = useState<{ row: DestinationHitRecord; anchor: HTMLElement } | null>(null)
  const [exception, setException] = useState<DestinationHitRecord | null>(null)
  useEffect(() => { const timer = window.setTimeout(() => setDebounced(keyword.trim()), 250); return () => window.clearTimeout(timer) }, [keyword])
  const hits = useDestinationHits(filters, debounced, !timeDraft || !!timeDraft.since && !!timeDraft.until), data = hits.data
  const time = useMemo(() => hitTime(i18n?.language ?? 'en-US', timezone), [i18n?.language, timezone])
  const patch = (change: Partial<RecordsFilters>) => setParams(prev => recordsSearch(prev, change, retentionDays), { replace: true, state: location.state })
  const editRange = (range: { since: string; until: string }) => {
    if (range.since && range.until && recordsRequest({ ...parsed, ...range }, '', Date.now())) { setTimeDraft(null); patch(range) }
    else setTimeDraft(range)
  }
  const clear = () => { setTimeDraft(null); setKeyword(''); setDebounced(''); patch({ user_id: undefined, panel_id: undefined, source: undefined, action: undefined, since: '24h', until: undefined, include_trial: false, page: 1 }) }
  const sourceNames = new Map<string, string | null>()
  for (const row of [...definitions?.allow ?? [], ...definitions?.block ?? [], ...definitions?.observe ?? []]) sourceNames.set(`p${row.id}`, row.name)
  for (const source of data?.sources ?? []) sourceNames.set(source.source, source.name)
  if (filters.source && !sourceNames.has(filters.source)) sourceNames.set(filters.source, null)
  const groups = [
    { key: 'policies', rows: [...sourceNames].filter(([key, name]) => key.startsWith('p') && name !== null) },
    { key: 'groups', rows: [...sourceNames].filter(([key, name]) => key.startsWith('g') && name !== null) },
    { key: 'deleted', rows: [...sourceNames].filter(([, name]) => name === null) },
  ]
  const days = Math.max(1, Math.min(7, retentionDays || 30)), relative = !timeDraft && /^(1h|24h|[1-9]\d*d)$/.test(filters.since)
  const filtered = !!(filters.user_id || filters.panel_id || filters.source || filters.action || keyword.trim() || filters.include_trial)
  const pageCount = Math.max(1, Math.ceil((data?.total ?? 0) / filters.page_size))
  const extraFilters = <>
    <TextField select slotProps={selectProps} size="small" label={t(`${P}source`)} value={filters.source ?? ''} sx={{ minWidth: 180, maxWidth: '100%', flex: 1 }} onChange={e => patch({ source: e.target.value || undefined })}>
      <MenuItem value="">{t(`${P}all`)}</MenuItem>{groups.flatMap(group => group.rows.length ? [<ListSubheader key={group.key}>{t(`${P}${group.key}`)}</ListSubheader>, ...group.rows.map(([source, name]) => <MenuItem key={source} value={source}>{name ?? `${t(`${P}deleted`)} ${source}`}</MenuItem>)] : [])}
    </TextField>
    <TextField select slotProps={selectProps} size="small" label={t(`${P}panel`)} value={filters.panel_id ?? ''} sx={{ minWidth: 140, maxWidth: '100%', flex: 1 }} onChange={e => patch({ panel_id: e.target.value ? Number(e.target.value) : undefined })}>
      <MenuItem value="">{t(`${P}all`)}</MenuItem>{filters.panel_id && !status?.nodes.some(node => node.panel_id === filters.panel_id) && <MenuItem value={filters.panel_id}>#{filters.panel_id}</MenuItem>}{status?.nodes.map(node => <MenuItem key={node.panel_id} value={node.panel_id}>{node.panel_name}</MenuItem>)}
    </TextField>
    <TextField size="small" label={t(`${P}search`)} value={keyword} sx={{ minWidth: 180, maxWidth: '100%', flex: 1 }} onChange={e => { setKeyword(e.target.value); patch({ page: 1 }) }} />
  </>
  const detailDays: Array<{ day: string; hour: number; rows: DestinationHitRecord[] }> = []
  if (data?.group_by === 'none') for (const row of data.items) {
    const day = time.day(row.hour), group = detailDays.at(-1)
    if (group?.day === day) group.rows.push(row)
    else detailDays.push({ day, hour: row.hour, rows: [row] })
  }
  return <Stack spacing={2} sx={{ minWidth: 0, '& button, & .MuiMenuItem-root': { minHeight: 44, minWidth: 44 }, '& .MuiAutocomplete-popupIndicator, & .MuiAutocomplete-clearIndicator': { minWidth: 44 }, '& .MuiInputBase-root': { minHeight: 44 } }}>
    <HitsMetricCards summary={data?.summary} action={filters.action} onAction={action => patch({ action })} />
    <Stack direction="row" sx={{ gap: 1.5, flexWrap: 'wrap', alignItems: 'center', minWidth: 0 }}>
      <Box sx={{ width: { xs: '100%', sm: 260 }, minWidth: 0, '& .MuiAutocomplete-inputRoot': { pr: '90px !important' }, '& .MuiAutocomplete-endAdornment': { top: '50%', transform: 'translateY(-50%)' } }}><UserAutocomplete value={filters.user_id ?? null} onChange={id => patch({ user_id: id ?? undefined })} label={t(`${P}account`)} width={mobile ? 1000 : 260} optionMinHeight={44} /></Box>
      <TextField select slotProps={selectProps} size="small" label={t(`${P}time`)} value={relative ? filters.since : 'custom'} sx={{ width: { xs: '100%', sm: 190 }, minWidth: 0 }} onChange={e => { setTimeDraft(null); patch(e.target.value === 'custom' ? { since: localInput(Date.now() - 86400000), until: localInput(Date.now()) } : { since: e.target.value, until: undefined }) }}>
        <MenuItem value="1h">{t(`${P}hour`)}</MenuItem><MenuItem value="24h">{t(`${P}day`)}</MenuItem>{days > 1 && <MenuItem value={`${days}d`}>{t(`${P}days`, { days })}</MenuItem>}{relative && !['1h', '24h', `${days}d`].includes(filters.since) && <MenuItem value={filters.since}>{filters.since}</MenuItem>}<MenuItem value="custom">{t(`${P}custom`)}</MenuItem>
      </TextField>
      {mobile ? <Badge badgeContent={Number(!!filters.source) + Number(!!filters.panel_id) + Number(!!keyword.trim())} color="primary"><Button onClick={() => setMore(true)}>{t(`${P}filters`)}</Button></Badge> : extraFilters}
    </Stack>
    {!relative && <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}><TextField type="datetime-local" label={t(`${P}since`)} value={filters.since} slotProps={{ inputLabel: { shrink: true } }} onChange={e => editRange({ since: e.target.value, until: filters.until ?? '' })} /><TextField type="datetime-local" label={t(`${P}until`)} value={filters.until ?? ''} slotProps={{ inputLabel: { shrink: true } }} onChange={e => editRange({ until: e.target.value, since: filters.since })} /><Typography variant="caption">{t(`${P}browser_time`)}</Typography></Stack>}
    <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between' }}>
      <ToggleButtonGroup exclusive value={filters.group_by} aria-label={t(`${P}view`)} sx={{ flexWrap: 'wrap' }} onChange={(_, group_by: RecordsFilters['group_by'] | null) => { if (group_by) patch({ group_by }) }}>{(['none', 'site', 'user', 'policy'] as const).map(key => <ToggleButton key={key} value={key}>{t(`${P}group_${key}`)}</ToggleButton>)}</ToggleButtonGroup>
      <Typography variant="caption">{data ? t(`${P}total`, { count: data.total }) : '—'}</Typography><Button onClick={() => void hits.refetch()} disabled={!hits.valid || hits.isFetching}>{t(`${P}refresh`)}</Button>
    </Stack>
    {!hits.valid ? <Alert severity="error">{t(`${P}invalid_range`)}</Alert> : hits.error && !data ? <Alert aria-label={t(`${P}failed`)} severity="error" action={<Button onClick={() => void hits.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}failed`)}</Alert> : <>
      {hits.error && data && <Alert severity="warning" action={<Button onClick={() => void hits.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}stale`)}</Alert>}
      {hits.isFetching && <LinearProgress aria-label={t(`${P}loading`)} />}
      {data && <Alert severity={data.losses.rows || data.losses.events || data.losses.unmatched ? 'warning' : 'info'}><Typography variant="body2">{t(`${P}${data.losses.rows || data.losses.events || data.losses.unmatched ? 'loss_notice' : 'incomplete'}`)}</Typography><Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>{data.losses.rows > 0 && <Typography variant="caption">{t(`${P}loss_rows`, { count: data.losses.rows })}</Typography>}{data.losses.events > 0 && <Typography variant="caption">{t(`${P}loss_events`, { count: data.losses.events })}</Typography>}{data.losses.unmatched > 0 && <Typography variant="caption">{t(`${P}loss_unmatched`, { count: data.losses.unmatched })}</Typography>}</Stack></Alert>}
      {data?.total === 0 && <Paper variant="outlined" sx={{ p: 3 }}><Typography>{t(`${P}${filtered ? 'empty_filtered' : 'empty'}`)}</Typography><Typography variant="body2">{t(`${P}retention`, { days: retentionDays })}</Typography>{filtered && <Button onClick={clear}>{t(`${P}clear`)}</Button>}</Paper>}
      {data && data.total > 0 && <Paper variant="outlined" sx={{ minWidth: 0, overflow: 'clip' }}>
        <Box sx={{ p: 1.5, display: { xs: 'none', lg: 'grid' }, gap: 1, gridTemplateColumns: data.group_by === 'none' ? '130px minmax(100px,1fr) minmax(260px,2fr) minmax(90px,.7fr) 65px 44px' : 'minmax(0,1fr) 150px 120px 160px' }}>
          {(data.group_by === 'none' ? ['period', 'account', 'destination', 'panel', 'count'] : ['object', 'count', data.group_by === 'user' ? 'source_count_label' : 'user_count_label', 'recent']).map(key => <Typography key={key} variant="caption">{t(`${P}${key}`)}</Typography>)}
        </Box>
        {data.group_by === 'none' ? detailDays.map(group => <Box key={group.day}><Typography component="h3" variant="subtitle2" sx={{ p: 1.5, position: 'sticky', top: 0, bgcolor: 'background.paper', zIndex: 1 }}>{time.date(group.hour)}</Typography>{group.rows.map(row => <HitRow key={`${row.hour}/${row.panel_id}/${row.user_id}/${row.source}/${row.action}/${row.dest}/${row.port}`} row={row} time={time} onUser={onUser} onSource={source => patch({ source })} onMenu={anchor => setMenu({ anchor, row })} />)}</Box>) : data.items.map(row => <Box key={row.key} sx={{ p: 1.5, display: 'grid', alignItems: 'center', gap: 1, gridTemplateColumns: { xs: 'minmax(0,1fr) 100px', lg: 'minmax(0,1fr) 150px 120px 160px' }, borderBottom: 1, borderColor: 'divider' }}>
          {data.group_by === 'user' && Number(row.key) > 0 ? <Button sx={{ textTransform: 'none', justifyContent: 'flex-start', overflowWrap: 'anywhere', textAlign: 'left' }} onClick={() => onUser(Number(row.key))}>{row.name ?? t(`${P}deleted_account`, { id: row.key })}</Button> : data.group_by === 'policy' ? <Button sx={{ textTransform: 'none', justifyContent: 'flex-start' }} onClick={() => patch({ source: row.key })}>{row.name ?? `${t(`${P}deleted`)} ${row.key}`}</Button> : <Typography sx={{ overflowWrap: 'anywhere' }}>{row.key === '0' ? t(`${P}group_level`) : row.key}</Typography>}
          <Box sx={{ minWidth: 0 }}><Typography sx={{ textAlign: 'right', fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere' }}>{number(row.count)}</Typography><Box sx={{ height: 4, bgcolor: 'primary.main', borderRadius: 1, width: `${100 * row.count / Math.max(1, ...data.items.map(item => item.count))}%` }} /></Box>
          <Typography variant="caption" sx={{ display: { xs: 'none', lg: 'block' } }}>{t(`${P}${data.group_by === 'user' ? 'source_count' : 'user_count'}`, { count: data.group_by === 'user' ? row.source_count : row.user_count })}</Typography><Typography variant="caption" sx={{ display: { xs: 'none', lg: 'block' } }}>{time.full(row.last_at)}</Typography>
        </Box>)}
      </Paper>}
      {data && data.total > 0 && data.items.length === 0 && <Alert severity="info" action={<Button onClick={() => patch({ page: 1 })}>{t(`${P}first_page`)}</Button>}>{t(`${P}page_empty`)}</Alert>}
      {data && <Stack direction="row" sx={{ flexWrap: 'wrap', alignItems: 'center', justifyContent: 'flex-end', gap: 1 }}><IconButton aria-label={t(`${P}previous`)} disabled={filters.page <= 1} onClick={() => patch({ page: filters.page - 1 })}><ChevronLeftIcon /></IconButton><Typography>{number(filters.page)} / {number(pageCount)}</Typography><IconButton aria-label={t(`${P}next`)} disabled={filters.page >= pageCount} onClick={() => patch({ page: filters.page + 1 })}><ChevronRightIcon /></IconButton><TextField select slotProps={selectProps} size="small" label={t(`${P}page_size`)} value={filters.page_size} onChange={e => patch({ page_size: Number(e.target.value) })}>{RECORDS_PAGE_SIZES.map(size => <MenuItem key={size} value={size}>{number(size)}</MenuItem>)}</TextField></Stack>}
    </>}
    <Stack direction="row" sx={{ alignItems: 'center', gap: 1, flexWrap: 'wrap' }}><Typography variant="caption">{t(`${P}retention`, { days: retentionDays })}</Typography><Button onClick={onSettings}>{t(`${P}modify_retention`)}</Button></Stack>
    <Menu anchorEl={menu?.anchor} open={!!menu} onClose={() => setMenu(null)} slotProps={{ paper: { sx: { '& .MuiMenuItem-root': { minHeight: 44 } } } }}><MenuItem onClick={() => { if (menu) onTest(menu.row.dest); setMenu(null) }}>{t(`${P}test`)}</MenuItem><MenuItem onClick={() => { if (menu) setException(menu.row); setMenu(null) }}>{t(`${P}add_exception`)}</MenuItem><MenuItem disabled={!menu?.row.user_id} onClick={() => { if (menu?.row.user_id) onUser(menu.row.user_id); setMenu(null) }}>{t(`${P}open_account`)}</MenuItem></Menu>
    {exception && <AddExceptionDialog target={exception.dest} userId={exception.user_id || undefined} etaMs={status?.apply_eta_ms} onClose={() => setException(null)} />}
    <Dialog open={mobile && more} onClose={() => setMore(false)} fullWidth maxWidth="xs" aria-labelledby="record-filters-title" slotProps={{ paper: { sx: { '& .MuiInputBase-root': { minHeight: 44 }, '& button': { minWidth: 44, minHeight: 44 } } } }}><DialogTitle id="record-filters-title">{t(`${P}filters`)}</DialogTitle><DialogContent><Stack spacing={2} sx={{ pt: 1 }}>{extraFilters}</Stack></DialogContent><DialogActions><Button onClick={() => setMore(false)}>{t('common:actions.close')}</Button></DialogActions></Dialog>
  </Stack>
}
