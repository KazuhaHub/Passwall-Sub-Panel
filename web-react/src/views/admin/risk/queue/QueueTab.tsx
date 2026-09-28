import { useEffect, useRef, useState, type ReactNode } from 'react'
import {
  Alert, Box, Button, ButtonBase, Chip, IconButton, LinearProgress, Menu, MenuItem, Skeleton, Table, TableBody,
  TableCell, TableContainer, TableHead, TableRow, TextField, ToggleButton, ToggleButtonGroup, Tooltip, Typography,
  useMediaQuery, useTheme,
} from '@mui/material'
import MoreHorizIcon from '@mui/icons-material/MoreHoriz'
import { Link as RouterLink, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { PagedTableFooter } from '@/components/PagedTableFooter'
import {
  QUEUE_SOURCE_FILTERS, type QueueParams, type QueueRow, type QueueStatus, type QueueView,
} from '@/api/riskCenter'
import { useRiskQueue } from '@/query/riskCenter'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { activeDbIsCountryOnly } from '@/utils/geoAnomaly'
import { regionNamer } from '@/utils/regionName'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { listSeparator } from '@/utils/riskCenter'
import { availableActions, subjectOfRow } from '../actions/riskSubject'
import { useRiskActions } from '../actions/useRiskActions'
import { ReviewBadges } from '../drawer/DrawerHeader'
import { DetectorStateChip } from '../evidence/DetectorStateChip'
import { parseQueueParams, QUEUE_PAGE_SIZES, queueSearch } from '../riskParams'
import QueueMetricCards, { type MetricCard } from './QueueMetricCards'
import { headlineText, headlineTip, sourceChipLabel } from './queueText'

/** How long the typing must pause before the search is written. */
const SEARCH_DEBOUNCE_MS = 300

/** What each metric card sets; every card first clears what the others set,
 *  so one card is in force at a time. */
const CARD_FILTERS: Record<MetricCard, Partial<QueueParams>> = {
  flagged: { level: 'flagged' },
  suspect: { level: 'suspect' },
  // Every held account, whatever its review state (D17).
  auto: { auto_suspended: true, status: 'all' },
  dismissed: { status: 'dismissed' },
}
const NO_CARD: Partial<QueueParams> = { level: undefined, auto_suspended: false, status: 'open' }

/** The card whose filter is the one in force, or null. */
function activeCard(p: QueueParams): MetricCard | null {
  if (p.level && p.status === 'open' && !p.auto_suspended) return p.level
  if (p.auto_suspended && p.status === 'all' && !p.level) return 'auto'
  if (p.status === 'dismissed' && !p.level && !p.auto_suspended) return 'dismissed'
  return null
}

const STATUSES: readonly QueueStatus[] = ['open', 'dismissed', 'trusted', 'all']

/** A signal chip's colour by its level: the flag is the alarm, suspect the
 *  ramp. */
function levelColor(level: string): 'error' | 'warning' | 'default' {
  return level === 'flagged' ? 'error' : level === 'suspect' ? 'warning' : 'default'
}

/** What the failed read says: the server's words, else the transport's. */
function errorText(err: unknown): string {
  if (isAxiosError(err)) return String((err.response?.data as { error?: string } | undefined)?.error ?? err.message)
  return String(err)
}

/**
 * 待处理: ONE ROW PER ACCOUNT that needs a look, most urgent first (the
 * server sorts: flagged and held, flagged, held, suspect, then the latest
 * change). A row says what is wrong in one line and opens the account's
 * drawer; its ⋯ menu offers the same actions as the drawer, decided from the
 * row alone.
 *
 * Every filter lives in the URL (riskParams): the metric cards, the status,
 * the sources, the search, the bell's "needs action now" and the page. So
 * opening an account and coming Back, a reload, the bell or a copied link all
 * show the same list. Filters are written by REPLACE and each resets the
 * page.
 */
export default function QueueTab({ onOpenUser, onOpenLive, onOpenPolicy }: {
  onOpenUser: (userId: number) => void
  onOpenLive: () => void
  /** The policy tab, where the detectors' switches are. */
  onOpenPolicy: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const [urlParams, setUrlParams] = useSearchParams()
  const params = parseQueueParams(urlParams)
  const q = useRiskQueue(scope, params)
  // Advisory: a failed read shows no notice. No evidence of a dead database
  // is not evidence of one.
  const { data: geoip } = useGeoIPStatus(scope)
  const actions = useRiskActions()
  const [menu, setMenu] = useState<{ anchor: HTMLElement; row: QueueRow } | null>(null)

  const update = (patch: Partial<QueueParams>) => setUrlParams(prev => queueSearch(prev, patch), { replace: true })

  // The search box: the text is local, the URL is written once the typing
  // pauses. The write reads the URL as it is THEN (a ref kept current after
  // every render), so a card clicked while the timer waits is kept rather
  // than undone by a stale copy, and nothing is written when the text trims
  // to the search already in force. No effect depends on the input.
  const [search, setSearch] = useState(params.q ?? '')
  const current = useRef(urlParams)
  useEffect(() => { current.current = urlParams })
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => {
    const pending = timer
    return () => clearTimeout(pending.current)
  }, [])
  const onSearch = (value: string) => {
    setSearch(value)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      const now = current.current
      if ((now.get('q') ?? '') === value.trim()) return
      setUrlParams(queueSearch(now, { q: value }), { replace: true })
    }, SEARCH_DEBOUNCE_MS)
  }

  const card = activeCard(params)
  const toggleCard = (c: MetricCard) => update(card === c ? NO_CARD : { ...NO_CARD, ...CARD_FILTERS[c] })

  const data: QueueView | undefined = q.data
  const ctx = { nameRegion: regionNamer(t, i18n.language), panelTz, sep: listSeparator(i18n.language) }
  const dbDown = geoip !== undefined && (!geoip.enabled || !geoip.active)
  const status503 = isAxiosError(q.error) && q.error.response?.status === 503
  const narrowed = !!(params.source || params.level || params.auto_suspended || params.urgent || params.q)
    || (params.page ?? 1) > 1
  const cols = phone ? 4 : 7

  const signalChips = (row: QueueRow) => row.sources
    .filter(s => s.source !== 'geo_auto')
    .map(s => (
      <Chip key={s.source} size="small" variant="outlined" color={levelColor(s.level)}
        label={sourceChipLabel(s.source, row, t)} />
    ))

  // Relative in the list (a dual-timezone string per row would double the
  // column), exact in panel time in the tooltip. "—" when nothing changed on
  // record, never "just now".
  const changed = (row: QueueRow): ReactNode => (row.changed_at_ms > 0
    ? (
      <Tooltip title={formatMsDualTz(row.changed_at_ms, panelTz)}>
        <Typography component="span" sx={{ fontSize: 13, whiteSpace: 'nowrap' }}>
          {formatRelativeTimeShort(Date.now() - row.changed_at_ms, t)}
        </Typography>
      </Tooltip>
    )
    : <Typography component="span" sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>)

  const emptyText = narrowed
    ? t('admin:risk_center.queue.empty_filtered')
    : params.status === 'dismissed'
      ? t('admin:risk_center.queue.empty_dismissed')
      : params.status === 'trusted'
        ? t('admin:risk_center.queue.empty_trusted')
        : t('admin:risk_center.queue.empty')

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <QueueMetricCards counts={data?.counts} active={card} onToggle={toggleCard} onOpenLive={onOpenLive} />

      {/* Without a location database three detectors read "cannot tell",
          and an empty queue would look like a clean fleet. */}
      {dbDown && (
        <Alert severity="warning" sx={{ fontSize: 13 }}
          action={(
            <Button component={RouterLink} to="/admin/settings?tab=general" size="small" color="inherit">
              {t('admin:risk_center.queue.geoip_open')}
            </Button>
          )}>
          {t('admin:risk_center.queue.geoip_unavailable')}
        </Alert>
      )}
      {!dbDown && activeDbIsCountryOnly(geoip) && (
        <Alert severity="info" sx={{ fontSize: 13 }}>{t('admin:geo_anomalies.coarse_db')}</Alert>
      )}
      {(data?.counts.geo_unknown ?? 0) > 0 && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:risk_center.queue.geo_unknown', { count: data?.counts.geo_unknown })}
        </Typography>
      )}

      <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap', alignItems: 'center' }}>
        <ToggleButtonGroup exclusive size="small" value={params.status}
          aria-label={t('admin:risk_center.queue.filter_status')}
          onChange={(_, v: QueueStatus | null) => { if (v) update({ status: v }) }}>
          {STATUSES.map(s => (
            <ToggleButton key={s} value={s} sx={{ px: 1.5 }}>
              {t(`admin:risk_center.queue.status_${s}`)}
            </ToggleButton>
          ))}
        </ToggleButtonGroup>
        <TextField select size="small" label={t('admin:risk_center.queue.filter_source')} sx={{ minWidth: 200 }}
          value={params.source ? params.source.split(',') : []}
          onChange={e => {
            const v = e.target.value as unknown as string[] | string
            update({ source: (Array.isArray(v) ? v : v.split(',')).join(',') })
          }}
          slotProps={{
            select: {
              multiple: true,
              renderValue: sel => (
                <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
                  {(sel as string[]).map(s => (
                    <Chip key={s} size="small" label={t(`admin:risk_center.flags.source.${s}`)} />
                  ))}
                </Box>
              ),
            },
          }}>
          {QUEUE_SOURCE_FILTERS.map(s => (
            <MenuItem key={s} value={s}>{t(`admin:risk_center.flags.source.${s}`)}</MenuItem>
          ))}
        </TextField>
        <TextField size="small" label={t('admin:risk_center.queue.search')} value={search}
          onChange={e => onSearch(e.target.value)} sx={{ width: 220 }} />
        {params.urgent && (
          <Chip color="error" variant="outlined" label={t('admin:risk_center.queue.filter_urgent')}
            onDelete={() => update({ urgent: false })} />
        )}
      </Box>

      {q.error && (status503
        ? <Alert severity="info" sx={{ fontSize: 13 }}>{t('admin:risk_center.unwired')}</Alert>
        : (
          <Alert severity="error" sx={{ fontSize: 13 }}
            action={(
              <Button size="small" color="inherit" onClick={() => void q.refetch()}>
                {t('admin:risk_center.retry')}
              </Button>
            )}>
            {t('admin:risk_center.load_failed', { error: errorText(q.error) })}
          </Alert>
        ))}

      {/* A new filter keeps the previous page on screen while it loads; the
          bar says the list is being read again. */}
      <Box sx={{ height: 4 }}>{q.isFetching && data && <LinearProgress />}</Box>

      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('admin:risk_center.queue.col_user')}</TableCell>
              <TableCell>{t('admin:risk_center.queue.col_level')}</TableCell>
              {!phone && <TableCell>{t('admin:risk_center.queue.col_sources')}</TableCell>}
              <TableCell>{t('admin:risk_center.queue.col_reason')}</TableCell>
              {!phone && <TableCell>{t('admin:risk_center.queue.col_changed')}</TableCell>}
              {!phone && <TableCell>{t('admin:risk_center.queue.col_review')}</TableCell>}
              <TableCell />
            </TableRow>
          </TableHead>
          <TableBody>
            {q.isPending && [0, 1, 2].map(i => (
              <TableRow key={i}>
                <TableCell colSpan={cols}><Skeleton variant="text" /></TableCell>
              </TableRow>
            ))}
            {data && data.items.length === 0 && (
              <TableRow>
                <TableCell colSpan={cols}>
                  <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{emptyText}</Typography>
                  {/* Empty because nothing is judged, not because nobody is
                      sharing: say so, and offer the switches. */}
                  {!narrowed && params.status === 'open' && data.global_detectors_off && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', mt: 0.5 }}>
                      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
                        {t('admin:risk_center.queue.global_off')}
                      </Typography>
                      <Button size="small" onClick={onOpenPolicy}>{t('admin:risk_center.queue.open_policy')}</Button>
                    </Box>
                  )}
                </TableCell>
              </TableRow>
            )}
            {data?.items.map(row => {
              const name = row.upn || row.display_name || `#${row.user_id}`
              const tip = headlineTip(row, t)
              const headline = (
                <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 420 }}>
                  {headlineText(row, t, ctx)}
                </Typography>
              )
              return (
                <TableRow key={row.user_id} hover onClick={() => onOpenUser(row.user_id)} sx={{ cursor: 'pointer' }}>
                  <TableCell>
                    {/* The keyboard way in: focusable, Enter opens the
                        drawer, and focus comes back here when it closes. */}
                    <ButtonBase component="span" aria-label={t('admin:risk_center.queue.row_open', { upn: name })}
                      onClick={e => { e.stopPropagation(); onOpenUser(row.user_id) }}
                      sx={{ fontSize: 14, fontWeight: 600, borderRadius: 1, px: 0.25,
                        '&.Mui-focusVisible': { outline: `2px solid ${md.primary}` } }}>
                      {name}
                    </ButtonBase>
                    {(row.display_name || row.group_name) && (
                      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                        {[row.display_name, row.group_name].filter(Boolean).join(' · ')}
                      </Typography>
                    )}
                    {/* A phone folds the three narrow columns into one line
                        under the account. */}
                    {phone && (
                      <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap', alignItems: 'center', mt: 0.5 }}>
                        {signalChips(row)}
                        {changed(row)}
                        <ReviewBadges review={row.review} />
                      </Box>
                    )}
                  </TableCell>
                  <TableCell>
                    <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap', alignItems: 'center' }}>
                      {(row.level === 'flagged' || row.level === 'suspect') && <DetectorStateChip state={row.level} />}
                      {/* The detector's own hold, by its one name (D17); the
                          tooltip says since when. */}
                      {row.auto_suspended && (
                        <Tooltip title={t('admin:geo_anomalies.auto_suspended_since', {
                          time: formatMsDualTz(row.service_disabled_at_ms, panelTz),
                        })}>
                          <Chip size="small" color="error" label={t('admin:users.status.geo_auto')} />
                        </Tooltip>
                      )}
                    </Box>
                  </TableCell>
                  {!phone && (
                    <TableCell>
                      <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>{signalChips(row)}</Box>
                    </TableCell>
                  )}
                  <TableCell>{tip ? <Tooltip title={tip}>{headline}</Tooltip> : headline}</TableCell>
                  {!phone && <TableCell>{changed(row)}</TableCell>}
                  {!phone && (
                    <TableCell>
                      <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}><ReviewBadges review={row.review} /></Box>
                    </TableCell>
                  )}
                  <TableCell align="right" padding="checkbox">
                    <IconButton size="small" aria-label={t('admin:risk_center.actions.more')} aria-haspopup="menu"
                      onClick={e => { e.stopPropagation(); setMenu({ anchor: e.currentTarget, row }) }}>
                      <MoreHorizIcon fontSize="small" />
                    </IconButton>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </TableContainer>
      {data && (
        <PagedTableFooter total={data.total} page={params.page ?? 1} pageSize={params.page_size ?? 25}
          rowsPerPageOptions={QUEUE_PAGE_SIZES}
          onPageChange={p => update({ page: p })} onPageSizeChange={n => update({ page_size: n })} />
      )}

      {/* One menu for the table, outside every row: a click in it must not
          reach the row's own click and open the drawer behind it. The
          actions are the drawer's matrix, decided from the row — no read. */}
      <Menu anchorEl={menu?.anchor} open={menu !== null} onClose={() => setMenu(null)}>
        {menu && availableActions(subjectOfRow(menu.row), 'menu').map(k => (
          <MenuItem key={k} onClick={() => {
            const subject = subjectOfRow(menu.row)
            setMenu(null)
            actions.start(k, subject)
          }}>
            {t(`admin:risk_center.actions.${k}`)}
          </MenuItem>
        ))}
      </Menu>
      {actions.dialogs}
    </Box>
  )
}
