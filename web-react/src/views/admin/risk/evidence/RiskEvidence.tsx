import type { ReactNode } from 'react'
import { Box, Chip, Tooltip, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type {
  DevicesEvidence, LoginCountryEvidence, RiskSignal, SubSpreadEvidence, UsageShiftEvidence,
} from '@/api/riskSignals'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { countryFlag } from '@/utils/geo'
import { regionNamer } from '@/utils/regionName'
import { dayBits, dayLabels, formatGB, placeLabel, riskCodeText } from '@/utils/riskSignals'
import { DetectorStateChip } from './DetectorStateChip'

// The renderers of the four risk kinds' evidence, shared by every surface that
// shows one (the risk tab, the lookup, and the drawer and records after them),
// so one verdict never reads two ways. Field names are the server's wire
// contract (domain.*Evidence); every list is present, empty rather than null,
// but each renderer still defaults them: a record's params or an older row is
// read through the same code. Every absolute time is the panel's timezone.

/** One cell per window day, oldest first, each titled with its panel-local
 *  date. A filled cell is a day the place, client or device was seen on. */
export function DayStrip({ mask, labels }: { mask: number; labels: string[] }) {
  const md = useTheme().palette.md
  return (
    <Box data-testid="day-strip" sx={{ display: 'inline-flex', gap: '2px', flexShrink: 0 }}>
      {dayBits(mask, labels.length).map((on, i) => (
        <Box key={i} data-on={on ? 'true' : 'false'} title={labels[i]} sx={{
          width: 10, height: 10, borderRadius: '2px',
          bgcolor: on ? md.primary : md.surfaceContainerHighest, border: `1px solid ${md.outlineVariant}`,
        }} />
      ))}
    </Box>
  )
}

function Caption({ children }: { children: ReactNode }) {
  const md = useTheme().palette.md
  return <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{children}</Typography>
}

function Subtitle({ children }: { children: ReactNode }) {
  const md = useTheme().palette.md
  return <Typography sx={{ fontSize: 12, fontWeight: 600, color: md.onSurface, mt: 0.5 }}>{children}</Typography>
}

function Line({ children }: { children: ReactNode }) {
  return <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', fontSize: 13 }}>{children}</Box>
}

/** A place: the flag, then the name in its own element so the name reads (and
 *  is found) alone. */
function Place({ cc, name }: { cc: string; name: string }) {
  const flag = countryFlag(cc)
  return (
    <Box component="span" sx={{ minWidth: 140, display: 'inline-flex', gap: 0.5 }}>
      {flag && <span>{flag}</span>}
      <span>{name}</span>
    </Box>
  )
}

export function SubSpreadPanel({ ev }: { ev: SubSpreadEvidence }) {
  const { t, i18n } = useTranslation(['admin'])
  // Provinces only: a Chinese UI names a CN province by its ISO code, as the
  // Geo tab does. The foreign lines below stay country codes.
  const name = regionNamer(t, i18n.language)
  const labels = dayLabels(ev.window_start, ev.window_days)
  const provinces = ev.provinces ?? []
  const identities = ev.identities ?? []
  const foreign = ev.foreign ?? []
  const x = ev.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
  const cov = ev.coverage ?? { sources: 0, placed: 0, region_known: 0 }
  return (
    <>
      <Caption>{t('admin:risk_signals.window_hint', { start: ev.window_start, days: ev.window_days })}</Caption>
      {provinces.map((p, i) => (
        <Line key={`${p.cc}/${p.region}/${i}`}>
          <Place cc={p.cc} name={placeLabel(p, name)} />
          <DayStrip mask={p.days} labels={labels} />
          {p.established && (
            <Chip size="small" variant="outlined" color="primary" label={t('admin:risk_signals.province_recurring')} />
          )}
          <Chip size="small" variant="outlined" label={t('admin:risk_signals.province_group', { n: p.group })} />
        </Line>
      ))}
      {identities.length > 0 && <Subtitle>{t('admin:risk_signals.identities_title')}</Subtitle>}
      {identities.map((id, i) => {
        const places = (id.provinces ?? []).map(j => provinces[j]).filter(Boolean).map(p => placeLabel(p, name)).join(' · ')
        return (
          <Line key={`${id.kind}/${id.label}/${i}`}>
            <Chip size="small" variant="outlined" label={id.kind === 'hwid'
              ? t('admin:risk_signals.identity_hwid')
              : t('admin:risk_signals.identity_ua')} />
            <Box component="span" sx={{ minWidth: 140 }}>{id.label || (id.hwid4 ? `#${id.hwid4}` : '—')}</Box>
            <DayStrip mask={id.days} labels={labels} />
            {places && <Caption>{t('admin:risk_signals.identity_seen_at', { places })}</Caption>}
          </Line>
        )
      })}
      {/* Context only: sub_spread judges one country, and a landing egress
          abroad is the commonest reason a fetch shows up elsewhere. */}
      {foreign.length > 0 && <Subtitle>{t('admin:risk_signals.foreign_title')}</Subtitle>}
      {foreign.map(f => (
        <Line key={f.cc}>
          <Place cc={f.cc} name={f.cc} />
          <DayStrip mask={f.days} labels={labels} />
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.coverage', {
        sources: cov.sources, placed: cov.placed, region_known: cov.region_known,
        shared: x.shared, listed: x.listed, infra: x.infra, internal: x.internal,
      })}</Caption>
    </>
  )
}

export function DevicesPanel({ ev }: { ev: DevicesEvidence }) {
  const { t } = useTranslation(['admin'])
  const panelTz = useSiteStore(s => s.timezone)
  const labels = dayLabels(ev.window_start, ev.window_days)
  const devices = ev.devices ?? []
  const clients = ev.clients ?? []
  return (
    <>
      {devices.length > 0 && <Subtitle>{t('admin:risk_signals.devices_title')}</Subtitle>}
      {/* Only the digest's 4-character prefix is ever stored in evidence:
          enough to tell this account's devices apart, nothing more. */}
      {devices.map((d, i) => (
        <Line key={`${d.hwid4}/${i}`}>
          <Box component="span" sx={{ minWidth: 140 }}>{d.label || '—'}</Box>
          <Box component="span" sx={{ fontFamily: 'monospace', fontSize: 12 }}>{`#${d.hwid4}`}</Box>
          <DayStrip mask={d.days} labels={labels} />
          {d.recurrent && (
            <Chip size="small" variant="outlined" color="primary" label={t('admin:risk_signals.device_recurring')} />
          )}
          <Caption>{t('admin:risk_signals.device_last_seen', { time: formatMsDualTz(d.last_ms, panelTz) })}</Caption>
          {d.client && <Caption>{d.client}</Caption>}
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.device_fetches', { with: ev.fetches_with_hwid, without: ev.fetches_without })}</Caption>
      {clients.length > 0 && <Subtitle>{t('admin:risk_signals.clients_title')}</Subtitle>}
      {clients.map((c, i) => (
        <Line key={`${c.label}/${i}`}>
          <Box component="span" sx={{ minWidth: 140 }}>{c.label || '—'}</Box>
          <DayStrip mask={c.days} labels={labels} />
        </Line>
      ))}
    </>
  )
}

export function UsagePanel({ ev }: { ev: UsageShiftEvidence }) {
  const { t } = useTranslation(['admin'])
  // The numbers exist only once the judged days were judged; a warm-up or a
  // short history carries the series alone, and a caption of zeros would
  // read as a judgement.
  const judged = (ev.thresholds ?? []).length > 0
  // Both lengths are settings, so the title counts the bars it draws and
  // the caption the days the verdict judged. A row stored before they were
  // settings carries no recent_days and was judged over the shipped seven.
  const days = (ev.series ?? []).length
  const recent = ev.recent_days ?? 7
  return (
    <>
      <Caption>{t('admin:risk_signals.usage_title', { days, end: ev.end_date })}</Caption>
      <UsageBars ev={ev} />
      {judged && (
        <Caption>{t('admin:risk_signals.usage_caption', {
          median: formatGB(ev.median), ratio: ev.ratio, floor: formatGB(ev.floor), over: ev.over_days, recent,
        })}</Caption>
      )}
    </>
  )
}

/**
 * The series as bars, oldest first: the baseline days, then the judged ones.
 * Each judged day carries a tick at the threshold it was held to, and its bar
 * is drawn in the error colour when it went over — the thresholds already
 * include the fleet factor and the floor, so the tick is the line the day
 * actually crossed.
 */
export function UsageBars({ ev }: { ev: UsageShiftEvidence }) {
  const md = useTheme().palette.md
  const series = ev.series ?? []
  const thresholds = ev.thresholds ?? []
  const over = ev.over ?? []
  const n = series.length
  const firstJudged = n - thresholds.length
  const top = Math.max(1, ...series, ...thresholds)
  const H = 60
  const step = 8
  const endMs = Date.parse(`${ev.end_date}T00:00:00Z`)
  const start = Number.isNaN(endMs) ? '' : new Date(endMs - (n - 1) * 86_400_000).toISOString().slice(0, 10)
  const labels = dayLabels(start, n)
  const y = (b: number) => H - (b / top) * H
  return (
    <Box component="svg" viewBox={`0 0 ${Math.max(n * step, 1)} ${H}`} role="img"
      sx={{ width: '100%', maxWidth: 420, height: H, display: 'block' }}>
      {series.map((b, i) => {
        const j = i - firstJudged
        return (
          <rect key={i} x={i * step} y={y(b)} width={step - 2} height={H - y(b)}
            fill={j >= 0 && over[j] ? md.error : md.primary} opacity={j >= 0 ? 1 : 0.55}>
            <title>{`${labels[i] ? `${labels[i]}: ` : ''}${formatGB(b)}`}</title>
          </rect>
        )
      })}
      {thresholds.map((th, j) => {
        const x = (firstJudged + j) * step
        return <line key={j} x1={x - 1} x2={x + step - 1} y1={y(th)} y2={y(th)} stroke={md.onSurface} strokeWidth={1.5} />
      })}
    </Box>
  )
}

export function LoginPanel({ ev }: { ev: LoginCountryEvidence }) {
  const { t } = useTranslation(['admin'])
  const panelTz = useSiteStore(s => s.timezone)
  const known = ev.known ?? []
  const events = ev.events ?? []
  const s = ev.skipped ?? { infra: 0, internal: 0, listed: 0, node_country: 0, unplaced: 0 }
  // "Recent" is the hold, a per-group setting: the logins counted as recent
  // are the ones inside the hold this verdict was judged with.
  const hold = ev.hold_days ?? 7
  return (
    <>
      <Line>
        <Caption>{t('admin:risk_signals.login_known')}</Caption>
        {known.length
          ? known.map(cc => <Chip key={cc} size="small" variant="outlined" label={[countryFlag(cc), cc].filter(Boolean).join(' ')} />)
          : '—'}
      </Line>
      {events.length > 0 && <Subtitle>{t('admin:risk_signals.login_events')}</Subtitle>}
      {events.map((e, i) => (
        <Line key={`${e.cc}/${e.at_ms}/${i}`}>
          <Place cc={e.cc} name={e.cc} />
          <Caption>{formatMsDualTz(e.at_ms, panelTz)}</Caption>
          <Caption>{e.method}</Caption>
        </Line>
      ))}
      <Caption>{t('admin:risk_signals.login_counts', {
        lookback: ev.lookback_days, logins: ev.logins, recent: ev.recent, judged: ev.judged, hold,
        infra: s.infra, internal: s.internal, listed: s.listed, node_country: s.node_country, unplaced: s.unplaced,
      })}</Caption>
    </>
  )
}

/**
 * One kind's evidence by its kind: the one entry point, so a signal row and a
 * flag record's params render a kind the same way. Nothing for a kind this
 * build does not know or for a verdict with nothing to show (evidence null on
 * idle, disabled and exempt rows).
 */
export function RiskKindEvidence({ kind, evidence }: { kind: string; evidence: unknown }) {
  if (!evidence || typeof evidence !== 'object') return null
  switch (kind) {
    case 'sub_spread': return <SubSpreadPanel ev={evidence as SubSpreadEvidence} />
    case 'devices': return <DevicesPanel ev={evidence as DevicesEvidence} />
    case 'usage_shift': return <UsagePanel ev={evidence as UsageShiftEvidence} />
    case 'login_country': return <LoginPanel ev={evidence as LoginCountryEvidence} />
    default: return null
  }
}

/** One kind's cell: its state, explained by its code and its own time. A kind
 *  with no row is "not computed", never blank and never clean; in a table
 *  cell it is a dash with the words in its tooltip. */
export function RiskKindChip({ sig }: { sig: RiskSignal | undefined }) {
  const { t } = useTranslation(['admin'])
  const panelTz = useSiteStore(s => s.timezone)
  if (!sig) {
    return (
      <Tooltip title={t('admin:risk_center.state.not_computed')}>
        <Chip size="small" variant="outlined" label="—" />
      </Tooltip>
    )
  }
  const updated = t('admin:risk_signals.col_updated')
  return (
    <DetectorStateChip state={sig.state} code={sig.code}
      tooltip={`${riskCodeText(sig, t)} · ${updated} ${formatMsDualTz(sig.updated_at_ms, panelTz)}`} />
  )
}
