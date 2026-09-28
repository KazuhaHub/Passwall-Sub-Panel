import { useState, type ReactNode } from 'react'
import { Alert, Box, Chip, IconButton, Tooltip, Typography, useTheme } from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

import type { RiskReview, RiskUserSummary } from '@/api/riskCenter'
import { RISK_KINDS, type RiskKind, type RiskSignal } from '@/api/riskSignals'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { activeDbIsCountryOnly, reasonText, tierLabelKey } from '@/utils/geoAnomaly'
import { riskCodeText } from '@/utils/riskSignals'
import { flagSourceKey } from '@/utils/riskCenter'
import { DetectorStateChip } from '../evidence/DetectorStateChip'
import { GeoDistance, GeoPlaces } from '../evidence/GeoEvidence'
import { RiskKindEvidence } from '../evidence/RiskEvidence'

type GeoRow = NonNullable<RiskUserSummary['geo']>

/**
 * One detector: its name, its state in the one vocabulary, the sentence that
 * explains it, when it was last judged, whether that verdict is too old to
 * count, and — on demand — the evidence behind it.
 */
function DetectorRow({ title, chip, badges, reason, judgedAt, stale, evidence }: {
  title: string
  chip: ReactNode
  badges?: ReactNode
  reason?: string
  judgedAt?: number
  stale?: boolean
  evidence?: ReactNode
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const [open, setOpen] = useState(false)
  return (
    <Box data-testid="detector-row" component="section"
      sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 3, p: 1.5, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography data-testid="detector-title" component="h3" sx={{ fontSize: 14, fontWeight: 600, color: md.onSurface }}>
          {title}
        </Typography>
        {chip}
        {badges}
        <Box sx={{ flex: 1 }} />
        {evidence && (
          <Tooltip title={t(open ? 'admin:risk_signals.collapse' : 'admin:risk_signals.expand')}>
            <IconButton size="small" aria-expanded={open} onClick={() => setOpen(o => !o)}
              aria-label={t(open ? 'admin:risk_signals.collapse' : 'admin:risk_signals.expand')}>
              {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
            </IconButton>
          </Tooltip>
        )}
      </Box>
      {reason && <Typography sx={{ fontSize: 13, color: md.onSurface }}>{reason}</Typography>}
      {judgedAt ? (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:risk_center.drawer.last_judged', { time: formatMsDualTz(judgedAt, panelTz) })}
        </Typography>
      ) : null}
      {/* A verdict nobody re-judged within the freshness window is history:
          shown, but counted neither on the queue nor by the bell. */}
      {stale && (
        <Typography sx={{ fontSize: 12, color: md.tertiary }}>{t('admin:risk_center.drawer.stale_verdict')}</Typography>
      )}
      {open && evidence && (
        <Box sx={{ mt: 0.5, display: 'flex', flexDirection: 'column', gap: 0.75, fontSize: 13 }}>{evidence}</Box>
      )}
    </Box>
  )
}

/** The geo verdict's evidence: where, how far apart, and how many sources
 *  it stood on — a floor when a panel could not be read. */
function GeoEvidencePanel({ geo }: { geo: GeoRow }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  // Read only once the evidence is opened: it qualifies the places above.
  const { data: geoip } = useGeoIPStatus(scope)
  const ex = geo.evidence?.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
  return (
    <>
      <Box>
        <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:geo_anomalies.col_places')}
        </Typography>
        <Box sx={{ fontSize: 13 }}><GeoPlaces row={geo} /></Box>
      </Box>
      <GeoDistance evidence={geo.evidence} />
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, fontSize: 13 }}>
        <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:geo_anomalies.col_ips')}
        </Typography>
        <Tooltip title={t('admin:geo_anomalies.ips_hint', {
          concurrent: geo.concurrent_ips, window: geo.live_ips, excluded: geo.excluded_ips,
          shared: ex.shared, listed: ex.listed, infra: ex.infra, internal: ex.internal,
        })}>
          <span>{`${geo.concurrent_ips} / ${geo.live_ips}`}</span>
        </Tooltip>
        {/* A count taken while a panel was unreadable is a floor, and shown
            as a plain number it reads as the whole story. */}
        {!geo.complete && (
          <Tooltip title={t('admin:geo_anomalies.incomplete')}>
            <WarningAmberIcon fontSize="inherit" color="warning" />
          </Tooltip>
        )}
      </Box>
      {activeDbIsCountryOnly(geoip) && (
        <Alert severity="info" sx={{ fontSize: 13 }}>{t('admin:geo_anomalies.coarse_db')}</Alert>
      )}
    </>
  )
}

function GeoDetector({ geo }: { geo: RiskUserSummary['geo'] }) {
  const { t } = useTranslation(['admin'])
  const title = t('admin:risk_center.drawer.detector_geo')
  if (!geo) {
    return <DetectorRow title={title} chip={<DetectorStateChip state="not_computed" />}
      reason={t('admin:risk_center.drawer.no_geo')} />
  }
  const tierKey = tierLabelKey(geo.tier)
  return (
    <DetectorRow title={title}
      // A verdict on a floor of its sources is never drawn green, and trust
      // reads as trust (the why's code).
      chip={<DetectorStateChip state={geo.state} code={geo.evidence?.why?.code} complete={geo.complete} />}
      badges={<>
        {tierKey && (
          <Chip size="small" variant="outlined" color={geo.flagged ? 'error' : geo.state === 'suspect' ? 'warning' : 'default'}
            label={t(tierKey)} />
        )}
        {/* The latch outlives the state: an idle or unreadable sample freezes
            the streak, so a flagged account that went quiet is still flagged. */}
        {geo.flagged && geo.state !== 'flagged' && (
          <Chip size="small" variant="outlined" color="error" label={t('admin:geo_anomalies.latched')} />
        )}
      </>}
      // ALWAYS the reason: "no data" next to "no connections right now" says
      // why, where a bare chip would read as a detector that is broken.
      reason={reasonText(geo, t)}
      judgedAt={geo.updated_at_ms}
      stale={geo.stale}
      evidence={<GeoEvidencePanel geo={geo} />} />
  )
}

function KindDetector({ kind, sig }: { kind: RiskKind; sig: (RiskSignal & { stale: boolean }) | undefined }) {
  const { t } = useTranslation(['admin'])
  const title = t(`admin:risk_signals.kind.${kind}`)
  if (!sig) return <DetectorRow title={title} chip={<DetectorStateChip state="not_computed" />} />
  return (
    <DetectorRow title={title} chip={<DetectorStateChip state={sig.state} code={sig.code} />}
      reason={riskCodeText(sig, t)} judgedAt={sig.updated_at_ms} stale={sig.stale}
      evidence={sig.evidence ? <RiskKindEvidence kind={kind} evidence={sig.evidence} /> : undefined} />
  )
}

/** The dismissal and trust, as lines: who, when, the note, and what the
 *  reopen rule makes of the dismissal now. An admin since deleted is named
 *  by id. */
function ReviewLines({ review }: { review: RiskReview }) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const R = 'admin:risk_center.review.'
  const line = (text: string, color = md.onSurfaceVariant) => (
    <Typography key={text} sx={{ fontSize: 13, color }}>{text}</Typography>
  )
  const lines: ReactNode[] = []
  if (review.dismissed) {
    lines.push(line(t(`${R}dismissed_by`, {
      by: review.dismissed_by_upn || `#${review.dismissed_by}`, time: formatMsDualTz(review.dismissed_at_ms, panelTz),
    })))
    if (review.note) lines.push(line(t(`${R}note`, { note: review.note })))
    if (review.reopened) {
      // The list separator of the language: a Chinese list reads 「、」.
      const sep = i18n.language?.startsWith('zh') ? '、' : ', '
      const sources = review.escalated.map(s => t(flagSourceKey(s))).join(sep)
      lines.push(line(t(`${R}reopened`, { sources }), md.error))
    }
    if (review.lapsed) lines.push(line(t(`${R}lapsed`), md.error))
  }
  if (review.trusted) {
    lines.push(line(t(`${R}trusted_by`, {
      by: review.trusted_by_upn || `#${review.trusted_by}`, time: formatMsDualTz(review.trusted_at_ms, panelTz),
    })))
    lines.push(line(t('admin:risk_center.drawer.trust_scope')))
  }
  if (lines.length === 0) return null
  return <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, px: 0.5 }}>{lines}</Box>
}

/**
 * 概览: one row per detector, always the same five in the same order (the
 * concurrent-location verdict, then each risk kind), so a detector with no
 * verdict reads "not computed yet" in its place rather than going missing.
 */
export default function OverviewTab({ summary }: { summary: RiskUserSummary }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
      <GeoDetector geo={summary.geo} />
      {RISK_KINDS.map(k => (
        <KindDetector key={k} kind={k} sig={summary.signals.find(s => s.kind === k)} />
      ))}
      <ReviewLines review={summary.review} />
    </Box>
  )
}
