import { useState } from 'react'
import {
  Box, Chip, CircularProgress, IconButton, MenuItem, Table, TableBody, TableCell, TableContainer, TableHead, TableRow,
  TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import PersonSearchOutlinedIcon from '@mui/icons-material/PersonSearchOutlined'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { PagedTableFooter } from '@/components/PagedTableFooter'
import UserAutocomplete from '@/components/UserAutocomplete'
import { FLAG_EVENTS, FLAG_LEVELS, type FlagRecord, type FlagRecordParams } from '@/api/riskCenter'
import { useFlagRecords } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { FLAG_SOURCES, flagEventKey, flagLevelKey, flagSourceKey, flagText, userLabel } from '@/utils/riskCenter'

/**
 * The event chip's colour, by the level the record moved TO: into a flag or
 * a suspension is the alarm, into suspect the ramp, and every leave or lift
 * (level none) is neutral — a cleared flag is news, not good news.
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
function toInstant(local: string): string | undefined {
  if (!local) return undefined
  const ms = Date.parse(local)
  return Number.isNaN(ms) ? undefined : new Date(ms).toISOString()
}

export interface FlagRecordsTabProps {
  /** Fixes the list to one account (the lookup): no user filter or column. */
  userId?: number
  /** The lookup's form: no intro, a longer page. */
  compact?: boolean
  onOpenUser?: (userId: number) => void
}

/**
 * The flag records: each time an account's concurrent-location verdict or a
 * risk signal entered or left suspect or flagged, and each automatic
 * suspension applied or lifted, newest first, with the reason AT THE TIME.
 *
 * Every filter is the server's and part of the query key, so a page is a
 * page of the filtered records.
 */
export default function FlagRecordsTab({ userId, compact = false, onOpenUser }: FlagRecordsTabProps) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(compact ? 50 : 25)
  const [filterUser, setFilterUser] = useState<number | null>(null)
  const [source, setSource] = useState('')
  const [level, setLevel] = useState('')
  const [event, setEvent] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set())

  const who = userId ?? filterUser ?? undefined
  const sinceAt = toInstant(since)
  const untilAt = toInstant(until)
  const params: FlagRecordParams = {
    page, page_size: pageSize,
    ...(who ? { user_id: who } : {}),
    ...(source ? { source } : {}),
    ...(level ? { level } : {}),
    ...(event ? { event } : {}),
    ...(sinceAt ? { since: sinceAt } : {}),
    ...(untilAt ? { until: untilAt } : {}),
  }
  const { data, isPending, error } = useFlagRecords(scope, params)
  const rows = data?.items ?? []

  // Any filter change starts again at the first page.
  const filter = <T,>(set: (v: T) => void) => (v: T) => { set(v); setPage(1) }
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

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      {!compact && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:risk_center.flags.intro')}</Typography>
      )}
      <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
        {showUser && (
          <UserAutocomplete value={filterUser} onChange={filter(setFilterUser)}
            label={t('admin:risk_center.flags.col_user')} width={220} />
        )}
        <TextField select size="small" label={t('admin:risk_center.flags.filter_source')} sx={{ width: 170 }}
          value={source} onChange={e => filter(setSource)(e.target.value)}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {FLAG_SOURCES.map(s => <MenuItem key={s} value={s}>{t(flagSourceKey(s))}</MenuItem>)}
        </TextField>
        <TextField select size="small" label={t('admin:risk_center.flags.filter_level')} sx={{ width: 130 }}
          value={level} onChange={e => filter(setLevel)(e.target.value)}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {FLAG_LEVELS.map(l => <MenuItem key={l} value={l}>{t(flagLevelKey(l === 'cleared' ? '' : l))}</MenuItem>)}
        </TextField>
        <TextField select size="small" label={t('admin:risk_center.flags.filter_event')} sx={{ width: 190 }}
          value={event} onChange={e => filter(setEvent)(e.target.value)}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {FLAG_EVENTS.map(ev => <MenuItem key={ev} value={ev}>{t(flagEventKey(ev))}</MenuItem>)}
        </TextField>
        <TextField type="datetime-local" size="small" label={t('admin:risk_center.flags.filter_since')}
          value={since} onChange={e => filter(setSince)(e.target.value)} sx={{ width: 210 }}
          slotProps={{ inputLabel: { shrink: true } }} />
        <TextField type="datetime-local" size="small" label={t('admin:risk_center.flags.filter_until')}
          value={until} onChange={e => filter(setUntil)(e.target.value)} sx={{ width: 210 }}
          slotProps={{ inputLabel: { shrink: true } }} />
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
        <PagedTableFooter total={data.total} page={page} pageSize={pageSize}
          rowsPerPageOptions={[25, 50, 100]}
          onPageChange={setPage} onPageSizeChange={n => { setPageSize(n); setPage(1) }} />
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
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const hasParams = rec.params !== null && rec.params !== undefined
  return (
    <>
      <TableRow hover>
        <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{new Date(rec.at_ms).toLocaleString()}</TableCell>
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
        <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 460 }}>{flagText(rec, t)}</TableCell>
        <TableCell align="right">
          {hasParams && (
            <IconButton size="small" onClick={onToggle}
              aria-label={open ? t('admin:risk_center.flags.evidence_hide') : t('admin:risk_center.flags.evidence_show')}>
              {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
            </IconButton>
          )}
        </TableCell>
      </TableRow>
      {open && hasParams && (
        <TableRow>
          <TableCell colSpan={cols} sx={{ bgcolor: md.surfaceContainerLow }}>
            {/* Address-free: no producer writes an address into params
                (pinned server-side by JSON-walk tests), so it is shown raw. */}
            <Box component="pre" sx={{ m: 0, fontSize: 12, fontFamily: 'monospace', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {JSON.stringify(rec.params, null, 2)}
            </Box>
          </TableCell>
        </TableRow>
      )}
    </>
  )
}
