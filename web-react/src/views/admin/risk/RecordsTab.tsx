import { Fragment, useState } from 'react'
import {
  Badge, Box, Button, Chip, CircularProgress, IconButton, MenuItem, Popover, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import FilterListIcon from '@mui/icons-material/FilterList'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import PersonSearchOutlinedIcon from '@mui/icons-material/PersonSearchOutlined'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { PagedTableFooter } from '@/components/PagedTableFooter'
import UserAutocomplete from '@/components/UserAutocomplete'
import type { GeoEvidence } from '@/api/geoAnomalies'
import { FLAG_EVENTS, FLAG_LEVELS, type FlagRecord, type FlagRecordParams } from '@/api/riskCenter'
import { RISK_KINDS } from '@/api/riskSignals'
import { useFlagRecords } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import {
  FLAG_SOURCES, flagEventKey, flagLevelKey, flagSourceKey, flagText, userLabel, withNote,
} from '@/utils/riskCenter'
import { GeoDistance, GeoPlaces } from './evidence/GeoEvidence'
import { RiskKindEvidence } from './evidence/RiskEvidence'
import { paramRows } from './recordValues'
import { parseRecordsParams, RECORDS_PAGE_SIZES, recordsSearch, type RecordsFilters } from './riskParams'

/**
 * The event chip's colour, by the level the record moved TO: into a flag or
 * a suspension is the alarm, into suspect the ramp, and every leave or lift
 * (level none) is neutral — a cleared flag is news, not good news. A review
 * record has no level and is neutral too.
 */
function levelColor(level: string): 'error' | 'warning' | 'default' {
  switch (level) {
    case 'flagged':
    case 'suspended':
      return 'error'
    case 'suspect':
      return 'warning'
    default:
      return 'default'
  }
}

/** A datetime-local value (browser time) as the RFC 3339 instant the server
 *  takes; '' (or a half-typed value) as no bound. */
function toInstant(local: string | undefined): string | undefined {
  if (!local) return undefined
  const ms = Date.parse(local)
  return Number.isNaN(ms) ? undefined : new Date(ms).toISOString()
}

function isRiskKind(source: string): boolean {
  return (RISK_KINDS as readonly string[]).includes(source)
}

export interface RecordsTabProps {
  /** Fixes the list to one account (the drawer's 时间线): no user filter or
   *  column, and the filters are the instance's own, not the page's URL. */
  userId?: number
  /** The drawer's form: a longer page. */
  compact?: boolean
  onOpenUser?: (userId: number) => void
}

/**
 * 记录: each time an account's concurrent-location verdict or a risk signal
 * entered or left suspect or flagged, each automatic suspension applied or
 * lifted, and each admin dismiss or trust action, newest first, with the
 * reason AT THE TIME.
 *
 * Every filter is the server's and part of the query key, so a page is a
 * page of the filtered records. The bar holds what an admin filters by most —
 * who, which source, when; level and change sit behind 更多筛选, with a badge
 * counting the ones set, so a filter in force is never out of sight.
 *
 * The page's instance keeps its filters in the URL (`rec_*`, riskParams), so
 * opening an account, following a drawer link and coming Back, a reload or a
 * copied link shows the same filtered page; written by REPLACE, like every
 * filter on the page. The drawer's instance (`userId`) keeps the same params
 * in its own state: it lives as long as the drawer, and the page's URL is not
 * its to write.
 */
export default function RecordsTab({ userId, compact = false, onOpenUser }: RecordsTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const ownsUrl = userId === undefined
  const [urlParams, setUrlParams] = useSearchParams()
  // The drawer's list is longer by default: it is the account's whole story.
  const [localParams, setLocalParams] = useState(() => new URLSearchParams(compact ? 'rec_size=50' : ''))
  const filters = parseRecordsParams(ownsUrl ? urlParams : localParams)
  // recordsSearch starts any filter change again at the first page: page 3
  // of one filter is not a page of another.
  const update = (patch: Partial<RecordsFilters>) => {
    if (ownsUrl) setUrlParams(prev => recordsSearch(prev, patch), { replace: true })
    else setLocalParams(prev => recordsSearch(prev, patch))
  }
  const [moreAnchor, setMoreAnchor] = useState<HTMLElement | null>(null)
  const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set())

  const who = userId ?? filters.user_id
  const sinceAt = toInstant(filters.since)
  const untilAt = toInstant(filters.until)
  const params: FlagRecordParams = {
    page: filters.page, page_size: filters.page_size,
    ...(who ? { user_id: who } : {}),
    ...(filters.source ? { source: filters.source } : {}),
    ...(filters.level ? { level: filters.level } : {}),
    ...(filters.event ? { event: filters.event } : {}),
    ...(sinceAt ? { since: sinceAt } : {}),
    ...(untilAt ? { until: untilAt } : {}),
  }
  const { data, isPending, error } = useFlagRecords(scope, params)
  const rows = data?.items ?? []
  const hidden = (filters.level ? 1 : 0) + (filters.event ? 1 : 0)

  const toggle = (id: number) => setOpen(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  const showUser = userId === undefined
  const cols = showUser ? 6 : 5
  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''
  // datetime-local reads and writes the BROWSER's time, while every time the
  // page prints is the panel's: the bounds say which one they are.
  const browserTime = t('admin:risk_center.flags.browser_time')

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
        {showUser && (
          <UserAutocomplete value={filters.user_id ?? null} onChange={id => update({ user_id: id ?? undefined })}
            label={t('admin:risk_center.flags.col_user')} width={220} />
        )}
        <TextField select size="small" label={t('admin:risk_center.flags.filter_source')} sx={{ width: 170 }}
          value={filters.source ?? ''} onChange={e => update({ source: e.target.value || undefined })}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {FLAG_SOURCES.map(s => <MenuItem key={s} value={s}>{t(flagSourceKey(s))}</MenuItem>)}
        </TextField>
        <TextField type="datetime-local" size="small"
          label={withNote(t('admin:risk_center.flags.filter_since'), browserTime, i18n.language)}
          value={filters.since ?? ''} onChange={e => update({ since: e.target.value || undefined })}
          sx={{ width: 230 }} slotProps={{ inputLabel: { shrink: true } }} />
        <TextField type="datetime-local" size="small"
          label={withNote(t('admin:risk_center.flags.filter_until'), browserTime, i18n.language)}
          value={filters.until ?? ''} onChange={e => update({ until: e.target.value || undefined })}
          sx={{ width: 230 }} slotProps={{ inputLabel: { shrink: true } }} />
        <Badge badgeContent={hidden} color="primary">
          <Button size="small" variant="outlined" startIcon={<FilterListIcon fontSize="small" />}
            aria-haspopup="dialog" onClick={e => setMoreAnchor(e.currentTarget)}>
            {t('admin:risk_center.flags.more_filters')}
          </Button>
        </Badge>
        <Popover open={moreAnchor !== null} anchorEl={moreAnchor} onClose={() => setMoreAnchor(null)}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
          slotProps={{ paper: { sx: { p: 2, display: 'flex', flexDirection: 'column', gap: 1.5, width: 240 } } }}>
          <TextField select size="small" label={t('admin:risk_center.flags.filter_level')}
            value={filters.level ?? ''} onChange={e => update({ level: e.target.value || undefined })}>
            <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
            {FLAG_LEVELS.map(l => <MenuItem key={l} value={l}>{t(flagLevelKey(l === 'cleared' ? '' : l))}</MenuItem>)}
          </TextField>
          <TextField select size="small" label={t('admin:risk_center.flags.filter_event')}
            value={filters.event ?? ''} onChange={e => update({ event: e.target.value || undefined })}>
            <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
            {FLAG_EVENTS.map(ev => <MenuItem key={ev} value={ev}>{t(flagEventKey(ev))}</MenuItem>)}
          </TextField>
        </Popover>
      </Box>
      {err && <Typography color="error" sx={{ fontSize: 13 }}>{err}</Typography>}

      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:risk_center.flags.col_at')}</TableCell>
              {showUser && <TableCell>{t('admin:risk_center.flags.col_user')}</TableCell>}
              <TableCell>{t('admin:risk_center.flags.col_source')}</TableCell>
              <TableCell>{t('admin:risk_center.flags.col_event')}</TableCell>
              <TableCell>{t('admin:risk_center.flags.col_detail')}</TableCell>
              <TableCell />
            </TableRow>
          </TableHead>
          <TableBody>
            {isPending && (
              <TableRow><TableCell colSpan={cols}><CircularProgress size={20} /></TableCell></TableRow>
            )}
            {!isPending && !err && rows.length === 0 && (
              <TableRow><TableCell colSpan={cols}>
                <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.flags.empty')}</Typography>
              </TableCell></TableRow>
            )}
            {rows.map(r => (
              <FlagRow key={r.id} rec={r} showUser={showUser} cols={cols} open={open.has(r.id)}
                onToggle={() => toggle(r.id)} onOpenUser={onOpenUser} />
            ))}
          </TableBody>
        </Table>
      </TableContainer>
      {data && (
        <PagedTableFooter total={data.total} page={filters.page} pageSize={filters.page_size}
          rowsPerPageOptions={RECORDS_PAGE_SIZES}
          onPageChange={p => update({ page: p })} onPageSizeChange={n => update({ page_size: n })} />
      )}
    </Box>
  )
}

function FlagRow({ rec, showUser, cols, open, onToggle, onOpenUser }: {
  rec: FlagRecord
  showUser: boolean
  cols: number
  open: boolean
  onToggle: () => void
  onOpenUser?: (userId: number) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const hasParams = rec.params !== null && rec.params !== undefined
  return (
    <>
      <TableRow hover>
        <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{formatMsDualTz(rec.at_ms, panelTz)}</TableCell>
        {showUser && (
          <TableCell>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
              <span>{userLabel(rec)}</span>
              {onOpenUser && (
                <Tooltip title={t('admin:risk_center.open_user')}>
                  <IconButton size="small" onClick={() => onOpenUser(rec.user_id)}>
                    <PersonSearchOutlinedIcon fontSize="inherit" />
                  </IconButton>
                </Tooltip>
              )}
            </Box>
          </TableCell>
        )}
        <TableCell>
          <Chip size="small" variant="outlined" label={t(flagSourceKey(rec.source), { defaultValue: rec.source })} />
        </TableCell>
        <TableCell>
          <Chip size="small" color={levelColor(rec.level)} label={t(flagEventKey(rec.event), { defaultValue: rec.event })} />
        </TableCell>
        {/* The reason AT THE TIME, from the numbers stored with the record —
            not the account's current verdict, which may have moved since. */}
        <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 460 }}>
          {flagText(rec, t, i18n.language)}
        </TableCell>
        <TableCell align="right">
          {hasParams && (
            <IconButton size="small" onClick={onToggle} aria-expanded={open}
              aria-label={open ? t('admin:risk_center.flags.evidence_hide') : t('admin:risk_center.flags.evidence_show')}>
              {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
            </IconButton>
          )}
        </TableCell>
      </TableRow>
      {open && hasParams && (
        <TableRow data-testid="record-detail">
          <TableCell colSpan={cols} sx={{ bgcolor: md.surfaceContainerLow }}>
            <RecordDetail rec={rec} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

/**
 * One record, expanded: its sentence, then its numbers the way the rest of
 * the page shows them — a geo record's places and distance, a risk record's
 * evidence panel for its kind, and every named value as a row (paramRows:
 * labels, tiers, hold names, panel-time instants, never a raw code). The
 * params as sent are one click further, under 原始数据, for the case the
 * rows do not cover.
 */
function RecordDetail({ rec }: { rec: FlagRecord }) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const [raw, setRaw] = useState(false)
  const params = rec.params && typeof rec.params === 'object' ? rec.params as Record<string, unknown> : {}
  const geoEvidence = rec.source === 'geo' && params.evidence && typeof params.evidence === 'object'
    ? params.evidence as GeoEvidence
    : undefined
  // A risk record's params ARE its verdict's evidence, drawn by its kind's
  // panel; laid out as rows as well, they would say the same thing twice.
  const risk = isRiskKind(rec.source)
  const rows = risk ? [] : paramRows(rec, t, panelTz, i18n.language)
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, fontSize: 13, py: 0.5 }}>
      <Typography sx={{ fontSize: 13, color: md.onSurface }}>{flagText(rec, t, i18n.language)}</Typography>
      {geoEvidence && (
        <Box sx={{ fontSize: 13 }}>
          <GeoPlaces row={{ places: [], evidence: geoEvidence }} />
          <GeoDistance evidence={geoEvidence} />
        </Box>
      )}
      {risk && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, fontSize: 13 }}>
          <RiskKindEvidence kind={rec.source} evidence={rec.params} />
        </Box>
      )}
      {rows.length > 0 && (
        <Box component="dl" sx={{
          m: 0, display: 'grid', gridTemplateColumns: 'max-content 1fr', columnGap: 2, rowGap: 0.5, fontSize: 13,
        }}>
          {rows.map(r => (
            <Fragment key={r.key}>
              <Box component="dt" sx={{ color: md.onSurfaceVariant }}>{r.label}</Box>
              <Box component="dd" sx={{ m: 0, color: md.onSurface }}>{r.value}</Box>
            </Fragment>
          ))}
        </Box>
      )}
      <Box>
        <Button size="small" onClick={() => setRaw(v => !v)} aria-expanded={raw} sx={{ textTransform: 'none' }}>
          {raw ? t('admin:risk_center.flags.raw_hide') : t('admin:risk_center.flags.raw_show')}
        </Button>
      </Box>
      {/* Address-free: no producer writes an address into params (pinned
          server-side by JSON-walk tests), and a review record carries the
          admin's id and levels only — never a name, never the note. */}
      {raw && (
        <Box component="pre" sx={{ m: 0, fontSize: 12, fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {JSON.stringify(rec.params, null, 2)}
        </Box>
      )}
    </Box>
  )
}
