import { useTranslation } from 'react-i18next'
import type { DestNodeStatus } from '@/api/accessControl'
import type { NodeRelease } from '@/api/nodeReleases'
import type { Server } from '@/api/servers'
import { NODE_STATE_TONE } from '@/utils/accessControl'
import { nodeStateText } from '@/components/NodePolicyStatusRow'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { ipCapBadgeTone } from '@/utils/capabilities'
import { AddressSummary } from './AddressList'
import { serverAddresses } from './addresses'
import { ipCapTone, resourceTone, splitVersionIdentity, typeLabel, updateVersionOf, accessToneOf } from './serverModel'
import type { ProbeState } from './useServerProbes'

/**
 * Name first; the address(es) under it, clipped with the whole text in the title or the popover; the identity warning, when there is one, in words.
 * With `onOpen` the name line is a button that opens the server; the address trigger stays outside it (a button never holds another),
 * and its clicks stop there so they never reach a row that opens on click.
 */
export function NameCell({ s, onOpen }: { s: Server; onOpen?: () => void }) {
  const { t } = useTranslation('admin')
  const head = (
    <>
      <span className="min-w-0 truncate font-medium" title={s.name}>{s.name}</span>
      {s.client_identities_warning && (
        <span title={t('admin:servers.identities.warning_tip')} data-testid="client-identities-warning">
          <StatusBadge tone="warning" label={t('admin:servers.identities.warning')} />
        </span>
      )}
    </>
  )
  const reported = serverAddresses(s).reported
  return (
    <div className="flex min-w-0 flex-col">
      {onOpen
        ? <button type="button" onClick={onOpen} className="flex min-w-0 items-center gap-2 text-left outline-none focus-visible:underline">{head}</button>
        : <div className="flex min-w-0 items-center gap-2">{head}</div>}
      {/* The addresses the node reported replace the stored address line; a server with none (or only blank ones) keeps what it always showed. */}
      {reported
        ? <div onClick={onOpen ? e => e.stopPropagation() : undefined}><AddressSummary s={s} /></div>
        : <span className="truncate text-xs text-muted-foreground" title={s.url}>{s.url}</span>}
      {typeof s.client_identities === 'number' && (
        <span className="text-xs text-muted-foreground" data-testid="client-identities">{t('admin:servers.identities.count', { count: s.client_identities })}</span>
      )}
    </div>
  )
}

export function TypeCell({ s }: { s: Server }) {
  return <span className="whitespace-nowrap text-sm">{typeLabel(s)}</span>
}

/** How many nodes and lines the server carries; a dash while the nodes are unread, so it never claims zero. */
export function CountsCell({ counts }: { counts: { nodes: number; lines: number } | undefined }) {
  const { t } = useTranslation('admin')
  if (!counts) return <span className="text-muted-foreground">—</span>
  return <span className="whitespace-nowrap tabular-nums" title={t('admin:servers.counts.title', { nodes: counts.nodes, lines: counts.lines })}>{counts.nodes} / {counts.lines}</span>
}

function ProbeLine({ probe }: { probe: ProbeState }) {
  const { t } = useTranslation('admin')
  switch (probe.status) {
    case 'ok':
      return <StatusDot tone="success" label={typeof probe.inbound_count === 'number' ? t('admin:servers.status.ok_count', { count: probe.inbound_count }) : t('admin:servers.status.ok')} />
    case 'fail':
      return (
        <div className="flex min-w-0 flex-col" title={probe.error}>
          <StatusBadge tone="danger" label={t('admin:servers.status.fail')} />
          {probe.error && <span className="truncate text-xs text-muted-foreground">{probe.error}</span>}
        </div>
      )
    case 'checking':
      return <StatusDot tone="neutral" label={t('admin:servers.status.checking')} />
    case 'unconfigured':
      return <StatusDot tone="neutral" label={t('admin:servers.status.unconfigured')} />
    default:
      return <StatusDot tone="neutral" label={t('admin:servers.status.unknown')} />
  }
}

/**
 * Whether the server is reachable, and the other things about it that need a look: the concurrent-IP cap, the node's
 * resource health, its access-control state. A healthy state is a quiet dot; anything else says why in words.
 *
 * THE IP-CAP BADGE says whether a concurrent-IP cap pushed to this node is acted on. "enforced" renders nothing (the
 * clean case, like a supported version); "unsupported" (a panel older than 3.7.0, which cannot tell us) renders nothing
 * either, or it would be a permanent badge on every older node, un-actionable and quickly unread. "unknown" DOES render:
 * it means a panel that should have answered did not, so the detector itself is broken — and a broken detector that
 * looks like a clean fleet is the exact failure this probe exists to prevent.
 */
export function StatusCell({ s, probe, access, onOpenAccess }: {
  s: Server
  probe: ProbeState
  access: DestNodeStatus | undefined
  onOpenAccess: () => void
}) {
  const { t } = useTranslation('admin')
  const capTone = ipCapBadgeTone(s.ip_limit_enforcement)
  const capKey = s.ip_limit_enforcement === 'disabled' || s.ip_limit_enforcement === 'not_installed' ? 'dead'
    : s.ip_limit_enforcement === 'disconnect_only' ? 'disconnect_only' : 'unknown'
  const capTip = s.ip_limit_enforcement === 'disabled' ? 'disabled_tip' : s.ip_limit_enforcement === 'not_installed' ? 'not_installed_tip'
    : s.ip_limit_enforcement === 'disconnect_only' ? 'disconnect_only_tip' : 'unknown_tip'
  const resource = resourceTone(s)
  const accessText = access ? nodeStateText(t as never, access, Date.now()) : null
  return (
    <div className="flex min-w-0 flex-col items-start gap-1">
      <ProbeLine probe={probe} />
      {capTone !== 'none' && (
        <span title={t(`admin:servers.ip_cap.${capTip}`)}><StatusBadge tone={ipCapTone[capTone]} label={t(`admin:servers.ip_cap.${capKey}`)} /></span>
      )}
      {resource && <StatusBadge tone={resource.tone} label={t('admin:servers.resource_state', { state: t(`admin:nodeMetrics.freshness.${s.node_resource_health}`) })} />}
      {!!s.fronts_inactive && s.panel_type === 'psp' && (
        <span title={t('admin:servers.fronts.row_inactive', { count: s.fronts_inactive })}><StatusBadge tone="warning" label={t('admin:servers.fronts.row_inactive', { count: s.fronts_inactive })} /></span>
      )}
      {accessText && access && (
        <button type="button" data-testid="access-line" data-state={accessText.key} onClick={onOpenAccess}
          className="flex max-w-full items-center gap-1 rounded-sm text-left text-xs underline-offset-2 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
          <StatusDot tone={accessToneOf(NODE_STATE_TONE[accessText.key])} label={t('admin:servers.access.line', { state: accessText.label })} />
        </button>
      )}
    </div>
  )
}

// The sentence for a native node's compatibility state, chosen by REASON and not by status, because the status folds
// situations that need different sentences: "unknown" is both "never installed" and "the last report is too old to
// act on", and telling an operator to wait for a check-in is only true for the first. A refused node reports BOTH
// halves on purpose (the generation last accepted and the one refused now), so losing contact never erases the last
// valid observation.
function nativeCompatibilityDetail(s: Server, t: (k: string, o?: Record<string, unknown>) => string): string | undefined {
  if (!s.node_compatibility) return undefined
  const key = 'admin:servers.native.compatibility.'
  const since = s.node_refused_since ? new Date(s.node_refused_since).toLocaleString() : undefined
  switch (s.node_compatibility_reason) {
    case 'protocol-incompatible':
      return t(`${key}incompatible_detail`, {
        reported: s.node_refused_protocol_version ?? s.node_effective_protocol_version ?? '—',
        min: s.node_reviewed_protocol_min ?? '—', max: s.node_reviewed_protocol_max ?? '—',
        accepted: s.node_protocol_version ?? '—', since: since ?? '—',
      })
    case 'report-refused': return t(`${key}report_refused_detail`, { since: since ?? '—' })
    case 'observation-stale': return t(`${key}unknown_observation_stale`, { observed: s.node_protocol_observed_at ? new Date(s.node_protocol_observed_at).toLocaleString() : '—' })
    case 'unverified': return t(`${key}unknown_never_observed`)
    default: return t(`${key}${s.node_compatibility}_detail`)
  }
}

/**
 * The version, whether it is supported, and whether there is something newer. A supported 3X-UI and a compatible
 * Passwall Node are quiet (the version text alone); too old, untested, limited, unknown and incompatible each say so in
 * words, because they change the next action. Nothing "never probed" shows a dash, distinct from "probed and fine".
 */
export function VersionCell({ s, nodeReleases, nodeReleaseCheckFailed, suiReleaseVersion }: {
  s: Server
  nodeReleases: NodeRelease[] | null
  nodeReleaseCheckFailed: boolean
  suiReleaseVersion: string | null
}) {
  const { t } = useTranslation('admin')
  const native = s.panel_type === 'psp'
  const nativeStatusVisible = native && s.node_compatibility !== undefined && s.node_compatibility !== 'compatible'
  if (!s.panel_version && (!native || (!s.desired_core_version && !nativeStatusVisible))) return <span className="text-muted-foreground">—</span>
  const pendingCore = native && !!s.desired_core_version && (s.desired_core_engine !== s.core_engine || s.desired_core_version !== s.core_version)
  let badge: { tone: 'warning' | 'danger' | 'neutral'; label: string } | null = null
  if (native) {
    if (s.node_compatibility === 'limited') badge = { tone: 'warning', label: t('admin:servers.native.compatibility.limited') }
    else if (s.node_compatibility === 'incompatible') badge = { tone: 'danger', label: t('admin:servers.native.compatibility.incompatible') }
    else if (s.node_compatibility !== 'compatible') badge = { tone: 'neutral', label: t('admin:servers.native.compatibility.unknown') }
  } else if (s.compat_status === 'too_old') badge = { tone: 'danger', label: t('admin:servers.compat.too_old') }
  else if (s.compat_status === 'untested') badge = { tone: 'warning', label: t('admin:servers.compat.untested') }
  else if (s.compat_status !== 'supported') badge = { tone: 'neutral', label: t('admin:servers.compat.unknown') }
  const identity = splitVersionIdentity(s.panel_version)
  const message = native ? nativeCompatibilityDetail(s, t as never) : s.compat_message
  const heading = `${typeLabel(s)} ${identity.display}`
  const detail = [identity.display, identity.commit && `commit: ${identity.commit}`, message].filter(Boolean).join('\n')
  const core = native ? (s.core_version && `${s.core_engine === 'sing-box' ? 'sing-box' : 'Xray'} ${s.core_version}`) : (s.xray_version && `Xray ${s.xray_version}`)
  const update = updateVersionOf(s, nodeReleases, suiReleaseVersion)
  return (
    <div className="flex min-w-0 flex-col items-start gap-1">
      <div className="flex min-w-0 flex-col leading-tight">
        <span className={cn('truncate text-sm font-medium', detail && 'cursor-help')} title={detail || undefined}>{heading}</span>
        {core && <span className="truncate text-xs text-muted-foreground">{core}</span>}
        {pendingCore && <span className="text-xs text-muted-foreground">{t('admin:servers.field.core_pending', { engine: s.desired_core_engine === 'sing-box' ? 'sing-box' : 'Xray', version: s.desired_core_version })}</span>}
      </div>
      {badge && <StatusBadge tone={badge.tone} label={badge.label} quiet={false} />}
      {update && <Badge variant="secondary">{t('admin:servers.update_available', { latest: update })}</Badge>}
      {native && nodeReleaseCheckFailed && <span className="whitespace-normal text-xs text-muted-foreground">{t('admin:servers.native.update_check_failed')}</span>}
      {/* S-UI KEEPS A MANUAL UPGRADE, and saying so is the difference between "there is a newer release" and "there is
          something for you to do". Shown only when there is an update, so it reads as a step and not as a permanent caveat. */}
      {s.panel_type === 'sui' && !!update && <span className="whitespace-normal text-xs text-muted-foreground">{t('admin:servers.sui_update.manual_hint')}</span>}
    </div>
  )
}
