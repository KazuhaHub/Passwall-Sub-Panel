import type { ReactNode } from 'react'
import {
  Box, Button, Chip, CircularProgress, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Tooltip,
  Typography, useTheme,
} from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { GeoAnomaly } from '@/api/geoAnomalies'
import type { User } from '@/api/types'
import { RISK_KINDS } from '@/api/riskSignals'
import { useGeoAnomalyForUser } from '@/query/geoAnomalies'
import { useSubLogs } from '@/query/logs'
import { useLiveConnections } from '@/query/riskCenter'
import { useRiskSignalsForUser } from '@/query/riskSignals'
import { useUserUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { useUserDetail } from '@/query/users'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import { reasonText, tierLabelKey, type Translate } from '@/utils/geoAnomaly'
import { formatGB } from '@/utils/riskSignals'
import { accountStateOf, serviceStateOf } from '@/utils/userAccess'
import { UserActivity } from '../UserActivity'
import { UserServerUsage } from '../UserServerUsage'
import ConnectionHistoryTable from './ConnectionHistoryTable'
import FlagRecordsTab from './FlagRecordsTab'
import { stateColor } from './GeoAnomaliesTab'
import LiveConnectionList from './LiveConnectionList'
import { LiveEmpty, LiveRefreshButton, LiveSnapshotHeader } from './LiveConnectionsTab'
import { RiskEvidencePanel, RiskKindChip } from './RiskSignalsTab'

/** A read's failure as one line: the server's message when it gave one. */
function errorText(error: unknown): string {
  return isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error)
}

/**
 * EVERYTHING ABOUT ONE ACCOUNT: who it is, who it is connected as right now,
 * its verdicts, the sources it was judged from, the record of its flags, its
 * recent fetches and sign-ins, and its usage.
 *
 * Every section reads that account alone (`user_id` on every request),
 * never a fleet list picked apart here, so a lookup costs the same on a
 * fleet of ten or of ten thousand. Sections mount only once the account is
 * known to exist: a deleted one is "not found", and asking the other
 * endpoints about it would only fetch their answer to nobody.
 */
export default function UserLookupDetail({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const { data: user, isPending, error } = useUserDetail(scope, userId)

  if (isPending) return <Box sx={{ p: 2 }}><CircularProgress size={24} /></Box>
  if (error || !user) {
    const gone = isAxiosError(error) && error.response?.status === 404
    return (
      <Typography sx={{ fontSize: 13, color: gone ? md.onSurfaceVariant : md.error }}>
        {gone || !error ? t('admin:risk_center.lookup.not_found') : errorText(error)}
      </Typography>
    )
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Section title={t('admin:risk_center.lookup.section_overview')}><Overview user={user} /></Section>
      <Section title={t('admin:risk_center.lookup.section_live')}><LiveSection userId={userId} /></Section>
      <Section title={t('admin:risk_center.lookup.section_geo')}><GeoSection userId={userId} /></Section>
      <Section title={t('admin:risk_center.lookup.section_risk')}><RiskSection userId={userId} /></Section>
      <Section title={t('admin:risk_center.lookup.section_history')}><ConnectionHistoryTable userId={userId} /></Section>
      <Section title={t('admin:risk_center.lookup.section_flags')}><FlagRecordsTab userId={userId} compact /></Section>
      <Section title={t('admin:risk_center.lookup.section_fetches')}><FetchesSection userId={userId} /></Section>
      <Section title={t('admin:risk_center.lookup.section_logins')}><UserActivity userId={userId} showTitle={false} /></Section>
      <Section title={t('admin:risk_center.lookup.section_usage')}><UserServerUsage userId={userId} /></Section>
    </Box>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  const md = useTheme().palette.md
  return (
    <Box component="section" sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 3, p: 2 }}>
      <Typography component="h3" sx={{ fontSize: 15, fontWeight: 600, color: md.onSurface, mb: 1.5 }}>{title}</Typography>
      {children}
    </Box>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  const md = useTheme().palette.md
  return (
    <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap', fontSize: 13 }}>
      <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant, minWidth: 96 }}>{label}</Typography>
      {children}
    </Box>
  )
}

/** The Users page's words for the two access axes, so the lookup and the
 *  Users table never name one state two ways. */
function accountLabel(u: User, t: Translate): string {
  switch (accountStateOf(u)) {
    case 'active': return t('admin:users.status.account_active')
    case 'pending_approval': return t('admin:users.status.pending_approval')
    case 'pending_delete': return t('admin:users.status.pending_delete')
    case 'pending_email_verify': return t('admin:users.status.pending_email_verify')
    default: return t('admin:users.status.account_disabled')
  }
}

function serviceLabel(u: User, t: Translate): string {
  switch (serviceStateOf(u)) {
    case 'emergency_active': return t('admin:users.status.emergency_active')
    case 'traffic_exceeded': return t('admin:users.status.traffic_exhausted')
    case 'expired': return t('admin:users.status.expired')
    case 'blocked_client': return t('admin:users.status.blocked')
    case 'manual_suspended': return t('admin:users.status.service_suspended')
    case 'account_disabled': return t('admin:users.status.service_unavailable')
    default: return t('admin:users.status.service_active')
  }
}

function Overview({ user }: { user: User }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  // Period usage is not on the user row; a failed read leaves it "—" rather
  // than a zero that would read as "used nothing".
  const { data: usage } = useUserUsage(scope, user.id)
  const limit = user.traffic_limit_bytes > 0 ? formatGB(user.traffic_limit_bytes) : t('admin:users.detail.unlimited')
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: 16, fontWeight: 600 }}>{user.upn}</Typography>
        {user.display_name && <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{user.display_name}</Typography>}
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{`#${user.id}`}</Typography>
      </Box>
      <Field label={t('admin:users.table.role')}>{t(`admin:users.role.${user.role}`, { defaultValue: user.role })}</Field>
      <Field label={t('admin:users.table.status')}>
        <Chip size="small" variant="outlined" label={accountLabel(user, t)} />
        <Chip size="small" variant="outlined" label={serviceLabel(user, t)} />
        {/* Only the detector's own hold, as on the Geo tab: another reason
            is a person's decision and must not be credited to it. */}
        {user.service_disabled_reason === 'geo_auto' && (
          <Chip size="small" color="error" label={t('admin:geo_anomalies.auto_suspended')} />
        )}
      </Field>
      <Field label={t('admin:users.table.expire')}>{user.expire_date || t('admin:users.status.permanent')}</Field>
      <Field label={t('admin:users.detail.period_used')}>
        {`${usage ? formatGB(usage.period_used_bytes) : '—'} / ${limit}`}
      </Field>
      <Button size="small" variant="outlined" component={RouterLink}
        to={`/admin/traffic?tab=trend&scope=user&user=${user.id}`}
        sx={{ alignSelf: 'flex-start', mt: 0.5, textTransform: 'none' }}>
        {t('admin:risk_center.lookup.open_traffic')} →
      </Button>
    </Box>
  )
}

function LiveSection({ userId }: { userId: number }) {
  const scope = useQueryScope()
  // One account is one "page" of the live view: the server pages accounts.
  const { data, isPending, error } = useLiveConnections(scope, { user_id: userId, page: 1, page_size: 1 })
  if (isPending) return <CircularProgress size={20} />
  if (error) return <Typography color="error" sx={{ fontSize: 13 }}>{errorText(error)}</Typography>
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}><LiveRefreshButton /></Box>
      <LiveSnapshotHeader view={data} />
      {/* The account is the subject here, not a filter: its "none" is about
          it, and says so when a panel could not be read. */}
      {data.items.length === 0
        ? data.snapshot.taken_at && <LiveEmpty view={data} oneAccount />
        : <LiveConnectionList users={data.items} deviceWindowHours={data.device_window_hours}
            devicesUnavailable={data.devices_unavailable} initiallyOpen />}
    </Box>
  )
}

/**
 * The verdict chip's colour. A clean verdict judged while some panel could
 * not be read stands on a floor of the account's sources — "clean as far as
 * could be seen" — so it is never drawn green, the colour of a clean bill of
 * health. A suspect or flagged verdict on a floor is, if anything, an
 * understatement, and keeps its colour.
 */
function verdictColor(row: Pick<GeoAnomaly, 'state' | 'complete'>): ReturnType<typeof stateColor> {
  const c = stateColor(row.state)
  return !row.complete && c === 'success' ? 'default' : c
}

function GeoSection({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const { data: row, isPending, error } = useGeoAnomalyForUser(scope, userId)
  if (isPending) return <CircularProgress size={20} />
  if (error) return <Typography color="error" sx={{ fontSize: 13 }}>{errorText(error)}</Typography>
  if (!row) {
    return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.lookup.no_geo')}</Typography>
  }
  const tierKey = tierLabelKey(row.tier)
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
        <Chip size="small" color={verdictColor(row)}
          label={t(`admin:geo_anomalies.state_${row.state}`, { defaultValue: row.state })} />
        {tierKey && (
          <Chip size="small" variant="outlined" color={row.flagged ? 'error' : row.state === 'suspect' ? 'warning' : 'default'}
            label={t(tierKey, { defaultValue: row.tier })} />
        )}
        {/* The latch outlives the state, as on the Geo tab. */}
        {row.flagged && row.state !== 'flagged' && (
          <Chip size="small" variant="outlined" color="error" label={t('admin:geo_anomalies.latched')} />
        )}
        {row.service_disabled_reason === 'geo_auto' && (
          <Chip size="small" color="error" label={t('admin:geo_anomalies.auto_suspended')} />
        )}
      </Box>
      <Typography sx={{ fontSize: 13 }}>{reasonText(row, t)}</Typography>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {`${t('admin:geo_anomalies.col_places')}: ${row.places.length ? row.places.join(' · ') : '—'} · `}
        {`${t('admin:geo_anomalies.col_ips')}: ${row.concurrent_ips} / ${row.live_ips}`}
        {/* The Geo tab's floor mark, for the same reason: a count taken
            while a panel was unreadable is a floor, and shown as a plain
            number it reads as the whole story. */}
        {!row.complete && (
          <Tooltip title={t('admin:geo_anomalies.incomplete')}>
            <WarningAmberIcon fontSize="inherit" color="warning" sx={{ verticalAlign: 'middle', ml: 0.5 }} />
          </Tooltip>
        )}
        {` · ${t('admin:geo_anomalies.col_updated')}: ${row.updated_at_ms ? new Date(row.updated_at_ms).toLocaleString() : '—'}`}
      </Typography>
    </Box>
  )
}

function RiskSection({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const { data: row, isPending, error } = useRiskSignalsForUser(scope, userId)
  if (isPending) return <CircularProgress size={20} />
  if (error) return <Typography color="error" sx={{ fontSize: 13 }}>{errorText(error)}</Typography>
  if (!row) {
    return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.lookup.no_risk')}</Typography>
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      {/* By RISK_KINDS, as the tab's columns: each kind named beside its
          chip, and a kind with no row reads "not computed". */}
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1.5 }}>
        {RISK_KINDS.map(k => (
          <Box key={k} sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t(`admin:risk_signals.kind.${k}`)}
            </Typography>
            <RiskKindChip sig={row.signals.find(s => s.kind === k)} />
          </Box>
        ))}
      </Box>
      <RiskEvidencePanel row={row} />
    </Box>
  )
}

function FetchesSection({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const { data, isPending, error } = useSubLogs(scope, { user_id: userId, page: 1, page_size: 20 })
  if (isPending) return <CircularProgress size={20} />
  if (error) return <Typography color="error" sx={{ fontSize: 13 }}>{errorText(error)}</Typography>
  const items = data?.items ?? []
  if (items.length === 0) {
    return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
  }
  return (
    <TableContainer>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>{t('admin:logs.sub_table.at')}</TableCell>
            <TableCell>{t('admin:logs.sub_table.ip')}</TableCell>
            <TableCell>{t('admin:logs.sub_table.client_type')}</TableCell>
            <TableCell>{t('admin:logs.sub_table.device', { defaultValue: '设备' })}</TableCell>
            <TableCell>{t('admin:logs.sub_table.ua')}</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {items.map(r => (
            <TableRow key={r.id}>
              <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>{formatDualTz(r.accessed_at, panelTz)}</TableCell>
              <TableCell sx={{ fontSize: 12 }}>
                <Box sx={{ fontFamily: 'monospace' }}>{r.ip}</Box>
                {formatRegion(r.region) && (
                  <Box sx={{ fontSize: 11, color: md.onSurfaceVariant }}>{formatRegion(r.region)}</Box>
                )}
              </TableCell>
              <TableCell sx={{ fontSize: 12 }}>{r.client_type}</TableCell>
              {/* The declared device, admins only (this page is): the label,
                  else the digest's 4-character prefix. */}
              <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                {r.device_label || (r.device_id4 ? `#${r.device_id4}` : '—')}
              </TableCell>
              <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                <Tooltip title={r.ua}><span>{r.ua}</span></Tooltip>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  )
}
