import { useState, type ReactNode } from 'react'
import { IconButton } from '@/components/common/IconButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { StatusBadge } from '@/components/common/StatusBadge'
import { IconChevronDown, IconChevronUp, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'

import type { RiskReview, RiskUserSummary } from '@/api/riskCenter'
import { RISK_KINDS, type RiskKind, type RiskSignal } from '@/api/riskSignals'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { activeDbIsCountryOnly, reasonText, tierLabelKey } from '@/utils/geoAnomaly'
import { riskCodeText } from '@/utils/riskSignals'
import { flagSourceKey, listSeparator } from '@/utils/riskCenter'
import { useRiskT } from '../useRiskT'
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
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const [open, setOpen] = useState(false)
  const label = t(open ? 'admin:risk_signals.collapse' : 'admin:risk_signals.expand')
  return (
    <section data-testid="detector-row" className="flex flex-col gap-1 rounded-lg border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 data-testid="detector-title" className="text-sm font-medium">{title}</h3>
        {chip}
        {badges}
        <span className="flex-1" />
        {evidence && (
          <IconButton label={label} size="icon-sm" aria-expanded={open} onClick={() => setOpen(o => !o)}>
            {open ? <IconChevronUp /> : <IconChevronDown />}
          </IconButton>
        )}
      </div>
      {reason && <p className="text-[13px]">{reason}</p>}
      {judgedAt ? (
        <p className="text-xs text-muted-foreground">{t('admin:risk_center.drawer.last_judged', { time: formatMsDualTz(judgedAt, panelTz) })}</p>
      ) : null}
      {/* A verdict nobody re-judged within the freshness window is history: shown, but counted neither on
          the queue nor by the bell. */}
      {stale && <StatusBadge tone="warning" label={t('admin:risk_center.drawer.stale_verdict')} className="mt-0.5" />}
      {open && evidence && <div className="mt-1 flex flex-col gap-1.5 text-[13px]">{evidence}</div>}
    </section>
  )
}

/** The geo verdict's evidence: where, how far apart, and how many sources
 *  it stood on — a floor when a panel could not be read. */
function GeoEvidencePanel({ geo }: { geo: GeoRow }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  // Read only once the evidence is opened: it qualifies the places above.
  const { data: geoip } = useGeoIPStatus(scope)
  const ex = geo.evidence?.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
  return (
    <>
      <div>
        <span className="text-xs text-muted-foreground">{t('admin:geo_anomalies.col_places')}</span>
        <div className="text-[13px]"><GeoPlaces row={geo} /></div>
      </div>
      <GeoDistance evidence={geo.evidence} />
      <div className="flex items-center gap-1.5 text-[13px]">
        <span className="text-xs text-muted-foreground">{t('admin:geo_anomalies.col_ips')}</span>
        <span className="tabular-nums" title={t('admin:geo_anomalies.ips_hint', {
          concurrent: geo.concurrent_ips, window: geo.live_ips, excluded: geo.excluded_ips,
          shared: ex.shared, listed: ex.listed, infra: ex.infra, internal: ex.internal,
        })}>{`${geo.concurrent_ips} / ${geo.live_ips}`}</span>
        {/* A count taken while a server was unreadable is a floor, and shown as a plain number it reads
            as the whole story. */}
        {!geo.complete && (
          <span role="img" aria-label={t('admin:geo_anomalies.incomplete')} title={t('admin:geo_anomalies.incomplete')} className="text-warning-ink">
            <IconWarning className="size-3.5" />
          </span>
        )}
      </div>
      {activeDbIsCountryOnly(geoip) && (
        <Alert><AlertDescription>{t('admin:geo_anomalies.coarse_db')}</AlertDescription></Alert>
      )}
    </>
  )
}

function GeoDetector({ geo }: { geo: RiskUserSummary['geo'] }) {
  const { t } = useRiskT()
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
        {tierKey && <Badge variant={geo.flagged ? 'destructive' : 'outline'}>{t(tierKey)}</Badge>}
        {/* The latch outlives the state: an idle or unreadable sample freezes
            the streak, so a flagged account that went quiet is still flagged. */}
        {geo.flagged && geo.state !== 'flagged' && (
          <Badge variant="destructive">{t('admin:geo_anomalies.latched')}</Badge>
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

function KindDetector({ kind, sig, userId }: { kind: RiskKind; sig: (RiskSignal & { stale: boolean }) | undefined; userId: number }) {
  const { t } = useRiskT()
  const title = t(`admin:risk_signals.kind.${kind}`)
  if (!sig) return <DetectorRow title={title} chip={<DetectorStateChip state="not_computed" />} />
  return (
    <DetectorRow title={title} chip={<DetectorStateChip state={sig.state} code={sig.code} />}
      reason={riskCodeText(sig, t)} judgedAt={sig.updated_at_ms} stale={sig.stale}
      evidence={sig.evidence ? <RiskKindEvidence kind={kind} evidence={sig.evidence} userId={userId} /> : undefined} />
  )
}

/** The dismissal and trust, as lines: who, when, the note, and what the
 *  reopen rule makes of the dismissal now. An admin since deleted is named
 *  by id. */
function ReviewLines({ review }: { review: RiskReview }) {
  const { t, i18n } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const R = 'admin:risk_center.review.'
  const line = (text: string, danger = false) => (
    <p key={text} className={cn('text-[13px]', danger ? 'text-destructive' : 'text-muted-foreground')}>{text}</p>
  )
  const lines: ReactNode[] = []
  if (review.dismissed) {
    lines.push(line(t(`${R}dismissed_by`, {
      by: review.dismissed_by_upn || `#${review.dismissed_by}`, time: formatMsDualTz(review.dismissed_at_ms, panelTz),
    })))
    if (review.note) lines.push(line(t(`${R}note`, { note: review.note })))
    if (review.reopened) {
      const sources = review.escalated.map(s => t(flagSourceKey(s))).join(listSeparator(i18n.language))
      lines.push(line(t(`${R}reopened`, { sources }), true))
    }
    if (review.lapsed) lines.push(line(t(`${R}lapsed`), true))
  }
  if (review.trusted) {
    lines.push(line(t(`${R}trusted_by`, {
      by: review.trusted_by_upn || `#${review.trusted_by}`, time: formatMsDualTz(review.trusted_at_ms, panelTz),
    })))
    lines.push(line(t('admin:risk_center.drawer.trust_scope')))
  }
  if (lines.length === 0) return null
  return <div className="flex flex-col gap-1 px-1">{lines}</div>
}

/**
 * 概览: one row per detector, always the same six in the same order (the
 * concurrent-location verdict, then each risk kind), so a detector with no
 * verdict reads "not computed yet" in its place rather than going missing.
 */
export default function OverviewTab({ summary }: { summary: RiskUserSummary }) {
  return (
    <div className="flex flex-col gap-3">
      <GeoDetector geo={summary.geo} />
      {RISK_KINDS.map(k => (
        <KindDetector key={k} kind={k} sig={summary.signals.find(s => s.kind === k)} userId={summary.user.id} />
      ))}
      <ReviewLines review={summary.review} />
    </div>
  )
}
