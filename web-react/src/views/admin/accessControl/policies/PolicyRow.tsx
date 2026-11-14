import { useState, type ReactNode } from 'react'
import {
  Box, Button, ButtonBase, Divider, IconButton, ListItemText, Menu, MenuItem, Switch, Tooltip, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { accessTone, policyHitsView, policyStateTone, type PolicyHitsView } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { matchSummary, summaryText } from './matchSummary'

export type RowMove = 'up' | 'down' | 'top' | 'bottom'

/**
 * One policy: two lines. The first is the name and its badges, with the
 * switch and the menu at the right; the second says what it matches and for
 * whom. A click anywhere else opens the editor. A policy that is off is
 * drawn at 0.6 and says "paused" in words as well.
 */
export default function PolicyRow({
  policy, index, count, listNames, scopeText, busy, moving, highlighted, hitDays, onOpen, onToggle, onMove, onDuplicate, onDelete,
  onReport, onConvert, onViewRecords, now, notDeployed = 0,
}: {
  policy: DestPolicy
  /** 0-based, within its action's section. */
  index: number
  count: number
  listNames: Record<number, string>
  scopeText: string
  /** This row's switch is saving. */
  busy: boolean
  /** A reorder is in flight: no other move may start. */
  moving: boolean
  highlighted: boolean
  /** The window the hit counters are over (`hit_window_days`, already held to the retention). */
  hitDays: number
  onOpen: () => void
  onToggle: () => void
  onMove: (m: RowMove) => void
  onDuplicate: () => void
  onDelete: () => void
  /** "Change to report only" (a block policy that is not already). */
  onReport: () => void
  /** "Turn into a block…" (a report-only row). */
  onConvert: () => void
  /** Open the records filtered to this policy. */
  onViewRecords: () => void
  /** The instant the list was read at, so every row's "2 hours ago" is measured from the same one. */
  now?: number
  /** Nodes not running the report-only rules (`reportNotDeployedCount`). */
  notDeployed?: number
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  // The switch means "is it deployed": on and report-only are lit, off is not.
  const off = policy.state === 'off'
  const report = policy.state === 'report'
  const stateTone = policyStateTone(policy.state)
  const hits = policyHitsView(policy, hitDays, notDeployed)

  const summary = summaryText(t, matchSummary({
    listNames: policy.list_ids.map(id => listNames[id] ?? t('admin:access_control.policies.row.deleted_list')),
    cidrs: policy.inline.cidrs?.length ?? 0,
    private: policy.inline.private === true,
    bittorrent: (policy.inline.protocols ?? []).includes('bittorrent'),
    ports: policy.inline.ports ?? '',
    network: policy.inline.network ?? '',
  }))
  const notReady = policy.list_states.some(s => s.state === 'not_ready')
  const emptyList = policy.list_states.some(s => s.state === 'empty')
  const over = policy.note?.state === 'empty' && !notReady

  return (
    <Box data-policy-id={policy.id} data-testid="policy-row" onClick={onOpen}
      sx={{
        p: 1.5, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${highlighted ? md.primary : md.outlineVariant}`,
        cursor: 'pointer', transition: 'border-color .2s', minWidth: 0,
      }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, opacity: off ? 0.6 : 1 }}>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 0.75, flex: 1, minWidth: 0 }}>
          <Typography sx={{ fontSize: 14, fontWeight: 600, overflowWrap: 'anywhere' }}>{policy.name}</Typography>
          {/* A report-only policy blocks nothing: its badge is its state, never the block badge. */}
          {report
            ? <ToneBadge tone={accessTone(theme, 'would_deny')} label={t('admin:access_control.policies.state.report')} testId="policy-state-badge" data="report" />
            : <ToneBadge tone={accessTone(theme, policy.action === 'block' ? 'block' : 'allow')} label={t(`admin:access_control.policies.action.${policy.action}`)} />}
          {/* Report-only hits never count: the badge shows only while the policy blocks. */}
          {policy.action === 'block' && !report && policy.counts_as_risk === true && (
            <ToneBadge tone={accessTone(theme, 'none')} label={t('admin:access_control.policies.row.counts_as_risk')} testId="policy-risk-badge" />
          )}
          {off && stateTone && <ToneBadge tone={accessTone(theme, stateTone)} label={t('admin:access_control.policies.row.paused')} testId="policy-state-badge" data="off" />}
          {notReady && (
            <Tooltip title={t('admin:access_control.policies.row.not_ready_hint')}>
              <span><ToneBadge tone={accessTone(theme, 'attention')} label={t('admin:access_control.policies.row.not_ready')} /></span>
            </Tooltip>
          )}
          {!notReady && emptyList && <ToneBadge tone={accessTone(theme, 'none')} label={t('admin:access_control.policies.row.empty_list')} />}
          {policy.scope_missing && <ToneBadge tone={accessTone(theme, 'failing')} label={t('admin:access_control.policies.row.scope_missing_badge')} />}
          {over && <ToneBadge tone={accessTone(theme, 'none')} label={t('admin:access_control.policies.row.nothing_left')} />}
        </Box>
        <Box onClick={e => e.stopPropagation()} sx={{ display: 'flex', alignItems: 'center', flex: '0 0 auto' }}>
          {report && !phone && (
            <Button size="small" variant="outlined" onClick={onConvert} sx={{ mr: 1.5, whiteSpace: 'nowrap' }}
              aria-label={t('admin:access_control.policies.row.convert_aria', { name: policy.name })}>
              {t('admin:access_control.policies.row.convert')}
            </Button>
          )}
          {/* Disabled while saving, never swapped for a spinner: a swapped-out control drops keyboard focus. */}
          <Switch checked={!off} disabled={busy} onChange={onToggle}
            slotProps={{ input: { 'aria-label': t('admin:access_control.policies.row.toggle', { name: policy.name }), 'aria-busy': busy || undefined } }} />
          <IconButton aria-label={t('admin:access_control.policies.row.menu', { name: policy.name })} aria-haspopup="menu"
            onClick={e => setMenu(e.currentTarget)} sx={{ width: 44, height: 44 }}>
            <MoreVertIcon />
          </IconButton>
          <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
            {([
              ['edit', () => onOpen(), false],
              ['duplicate', onDuplicate, false],
              ['up', () => onMove('up'), moving || index === 0],
              ['down', () => onMove('down'), moving || index === count - 1],
              ['top', () => onMove('top'), moving || index === 0],
              ['bottom', () => onMove('bottom'), moving || index === count - 1],
            ] as const).map(([key, fn, disabled]) => (
              <MenuItem key={key} disabled={disabled} onClick={() => { setMenu(null); fn() }}>
                <ListItemText>{t(`admin:access_control.policies.row.menu_${key}`)}</ListItemText>
              </MenuItem>
            ))}
            {policy.action === 'block' && !report && (
              <MenuItem onClick={() => { setMenu(null); onReport() }}>
                <ListItemText>{t('admin:access_control.policies.row.menu_report')}</ListItemText>
              </MenuItem>
            )}
            {policy.action === 'block' && (
              <MenuItem onClick={() => { setMenu(null); onViewRecords() }}>
                <ListItemText>{t('admin:access_control.policies.row.menu_records')}</ListItemText>
              </MenuItem>
            )}
            <Divider />
            <MenuItem onClick={() => { setMenu(null); onDelete() }} sx={{ color: md.error }}>
              <ListItemText>{t('admin:access_control.policies.row.menu_delete')}</ListItemText>
            </MenuItem>
          </Menu>
        </Box>
      </Box>
      <Typography variant="body2" sx={{ mt: 0.25, color: md.onSurfaceVariant, opacity: off ? 0.6 : 1, overflowWrap: 'anywhere' }}>
        {[summary || t('admin:access_control.policies.row.no_condition'), scopeText].join(' · ')}
        {policy.scope_missing ? ` · ${t('admin:access_control.policies.row.scope_missing')}` : ''}
        {notReady ? ` · ${t('admin:access_control.policies.row.not_ready_detail')}` : ''}
      </Typography>
      {(hits.kind !== 'none' || (report && phone)) && (
        <Box onClick={e => e.stopPropagation()} sx={{ mt: 0.5, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', opacity: off ? 0.6 : 1 }}>
          <HitsLine hits={hits} onViewRecords={onViewRecords} now={now} />
          {report && phone && (
            <Button size="small" variant="outlined" onClick={onConvert} sx={{ minHeight: 44 }}
              aria-label={t('admin:access_control.policies.row.convert_aria', { name: policy.name })}>
              {t('admin:access_control.policies.row.convert')}
            </Button>
          )}
        </Box>
      )}
    </Box>
  )
}

/**
 * What a row says of its hits (plan S2). A count is a link to the records
 * filtered to the policy; a report-only count is drawn in the "would be
 * denied" tone, the one row of the closed table that means "what it WOULD do".
 * No recording is a dash with its reason, never a 0.
 */
function HitsLine({ hits, onViewRecords, now }: { hits: PolicyHitsView; onViewRecords: () => void; now?: number }) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const num = new Intl.NumberFormat(i18n.language)
  const R = 'admin:access_control.policies.row.'
  const ago = (ms: number | null) => (ms === null ? '' : formatRelativeTimeShort((now ?? Date.now()) - ms, t))
  const wrap = (kind: string, children: ReactNode) => (
    <Box data-testid="policy-hits" data-kind={kind} sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75, flexWrap: 'wrap', minWidth: 0 }}>{children}</Box>
  )
  const link = (label: string, aria: string) => (
    <Tooltip title={t(`${R}view_records`)}>
      <ButtonBase onClick={onViewRecords} aria-label={`${t(`${R}view_records`)}: ${aria}`} sx={{
        display: 'inline-flex', alignItems: 'center', fontSize: 13, color: md.onSurfaceVariant, borderRadius: 1, textAlign: 'start', minHeight: { xs: 44, sm: 24 },
        '&:hover': { color: md.onSurface }, '&:focus-visible': { outline: `2px solid ${md.primary}`, outlineOffset: 2 },
      }}>
        {label}<ChevronRightIcon sx={{ fontSize: 16 }} />
      </ButtonBase>
    </Tooltip>
  )
  const shedNote = (n: number | undefined) => (n ? t(`${R}not_deployed_nodes`, { count: n }) : '')
  const quiet = (text: string) => <Typography variant="body2" component="span" sx={{ color: md.onSurfaceVariant }}>{text}</Typography>
  switch (hits.kind) {
    case 'none': return null
    case 'unavailable':
      return wrap('unavailable', (
        <Tooltip title={t(`${R}hits_unavailable_tip`)}>
          <Typography variant="body2" component="span" tabIndex={0} aria-label={t(`${R}hits_unavailable_tip`)} sx={{ color: md.onSurfaceVariant }}>{t(`${R}hits_unavailable`)}</Typography>
        </Tooltip>
      ))
    case 'count': {
      const text = t(`${R}hits_count`, { days: hits.days, count: hits.count, n: num.format(hits.count) })
      return wrap('count', <>{link([text, hits.last !== null ? ago(hits.last) : ''].filter(Boolean).join(' · '), text)}</>)
    }
    case 'zero':
      return wrap('zero', <>{link([t(`${R}hits_zero`, { days: hits.days }), ago(hits.last)].filter(Boolean).join(' · '), t(`${R}hits_zero`, { days: hits.days }))}</>)
    case 'never':
      return wrap('never', <ToneBadge tone={accessTone(theme, 'none')} label={t(`${R}hits_never`)} />)
    case 'would_block': {
      const text = t(`${R}would_block`, { days: hits.days, count: hits.count, n: num.format(hits.count) }) + shedNote(hits.notDeployed)
      return wrap('would_block', (
        <Tooltip title={t(`${R}view_records`)}>
          <ButtonBase onClick={onViewRecords} aria-label={`${t(`${R}view_records`)}: ${text}`} sx={{ borderRadius: 4, minHeight: { xs: 44, sm: 0 } }}>
            <ToneBadge tone={accessTone(theme, 'would_deny')} label={text} />
          </ButtonBase>
        </Tooltip>
      ))
    }
    case 'would_block_none':
      return wrap('would_block_none', quiet(t(`${R}would_block_none`, { days: hits.days }) + shedNote(hits.notDeployed)))
  }
}
