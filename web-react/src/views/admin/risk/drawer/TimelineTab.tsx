import { useState } from 'react'
import {
  Box, Button, Chip, CircularProgress, IconButton, MenuItem, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { FlagRecord } from '@/api/riskCenter'
import { useFlagRecords } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { FLAG_SOURCES, flagEventKey, flagSourceKey, flagText } from '@/utils/riskCenter'
import { UserActivity } from '../../UserActivity'
import { levelColor, RecordDetail } from '../RecordsTab'
import { recordsSearch } from '../riskParams'

/** The newest records the drawer lists; the rest are one link away, on the
 *  Records page, rather than paged inside a side panel. */
const RECORDS_SHOWN = 50

/**
 * One record in two lines: when (short, the exact panel time in the
 * tooltip, as the queue's 最近变化 — a dual-timezone string would take the
 * line), which source and what changed; then the reason at the time. Its
 * evidence opens under it, drawn as the Records page draws it.
 */
function Entry({ rec }: { rec: FlagRecord }) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const [open, setOpen] = useState(false)
  const hasParams = rec.params !== null && rec.params !== undefined
  return (
    <Box data-testid="timeline-entry" sx={{ py: 1, borderTop: `1px solid ${md.outlineVariant}`, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Tooltip title={formatMsDualTz(rec.at_ms, panelTz)}>
          <Typography data-testid="timeline-time" component="span" sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>
            {rec.at_ms > 0 ? formatRelativeTimeShort(Date.now() - rec.at_ms, t) : '—'}
          </Typography>
        </Tooltip>
        <Chip size="small" variant="outlined" label={t(flagSourceKey(rec.source), { defaultValue: rec.source })} />
        <Chip size="small" color={levelColor(rec.level)} label={t(flagEventKey(rec.event), { defaultValue: rec.event })} />
        {hasParams && (
          <IconButton size="small" sx={{ ml: 'auto' }} onClick={() => setOpen(v => !v)} aria-expanded={open}
            aria-label={open ? t('admin:risk_center.flags.evidence_hide') : t('admin:risk_center.flags.evidence_show')}>
            {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
          </IconButton>
        )}
      </Box>
      {/* The reason AT THE TIME, from the numbers stored with the record. */}
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{flagText(rec, t, i18n.language)}</Typography>
      {open && hasParams && (
        <Box data-testid="record-detail" sx={{ px: 1.5, py: 0.5, borderRadius: 2, bgcolor: md.surfaceContainer }}>
          <RecordDetail rec={rec} sentence={false} />
        </Box>
      )}
    </Box>
  )
}

/**
 * 时间线: the account's flag records, newest first, then its recent panel
 * sign-ins, and an exact link to the whole auth log for it (by id: a search
 * by name would also match other accounts whose names contain it).
 *
 * The records are a list made for a side panel, not the Records page's
 * table: two lines an entry, and one filter, the source. Dates, levels,
 * changes and paging are the Records page's, one link away, filtered to this
 * account. The filter lives as long as the drawer; the page's URL is not its
 * to write.
 */
export default function TimelineTab({ userId, upn }: { userId: number; upn: string }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const [source, setSource] = useState('')
  const { data, isPending, error } = useFlagRecords(scope, {
    page: 1, page_size: RECORDS_SHOWN, user_id: userId, ...(source ? { source } : {}),
  })
  const rows = data?.items ?? []
  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''
  const records = `/admin/risk?${recordsSearch(new URLSearchParams({ tab: 'records' }), { user_id: userId })}`
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between' }}>
        <TextField select size="small" label={t('admin:risk_center.flags.filter_source')} sx={{ width: 170 }}
          value={source} onChange={e => setSource(e.target.value)}>
          <MenuItem value="">{t('admin:risk_center.live.opt_all')}</MenuItem>
          {FLAG_SOURCES.map(s => <MenuItem key={s} value={s}>{t(flagSourceKey(s))}</MenuItem>)}
        </TextField>
        <Button size="small" component={RouterLink} to={records} sx={{ textTransform: 'none' }}>
          {t('admin:risk_center.drawer.open_records')}
        </Button>
      </Box>
      <Box>
        {isPending && <CircularProgress size={18} />}
        {err && <Typography sx={{ fontSize: 13, color: md.error }}>{t('admin:risk_center.load_failed', { error: err })}</Typography>}
        {!isPending && !err && rows.length === 0 && (
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.flags.empty')}</Typography>
        )}
        {rows.map(r => <Entry key={r.id} rec={r} />)}
        {data && data.total > rows.length && (
          <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, pt: 0.5 }}>
            {t('admin:risk_center.drawer.records_capped', { shown: rows.length, total: data.total })}
          </Typography>
        )}
      </Box>
      <Box>
        <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600, color: md.onSurface }}>
          {t('admin:risk_center.drawer.logins_title')}
        </Typography>
        <UserActivity userId={userId} showTitle={false} />
      </Box>
      <Button size="small" component={RouterLink}
        to={`/admin/logs?tab=auth&user_id=${userId}&upn=${encodeURIComponent(upn)}`}
        sx={{ alignSelf: 'flex-start', textTransform: 'none' }}>
        {t('admin:risk_center.drawer.open_auth_logs')}
      </Button>
    </Box>
  )
}
