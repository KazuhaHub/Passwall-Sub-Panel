import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus, ResolveUnavailable } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { IconChevronRight } from '@/lib/icons'
import {
  limitKindKey, nodeRecording, nodeResolve, nodeStateKey, NODE_STATE_TONE, type AccessToneKind, type NodeStateKey,
} from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'

// THE ONE NODE-STATE COMPONENT (plan §7.0 #10). The coverage sheet, the servers
// page's status line dialog and (later) the server detail block all draw a node's
// delivery state through this, from the one table in utils/accessControl. The
// words are fixed per state (admin:access_control.node_state / node_note); no raw
// code ever reaches the page.

type T = (key: string, opts?: Record<string, unknown>) => string
const S = 'admin:access_control.'

/** The badge words and the explaining line for a node, resolved from the bundle. */
export function nodeStateText(t: T, node: DestNodeStatus, now: number): { key: NodeStateKey; label: string; note: string; reportNote: string | null } {
  const key = nodeStateKey(node, now)
  const num = (n: number | undefined) => new Intl.NumberFormat().format(n ?? 0)
  const label = t(`${S}node_state.${key}`, { minutes: node.pending_since ? Math.max(1, Math.floor((now - node.pending_since) / 60000)) : 0 })
  let note: string
  switch (key) {
    case 'applied':
      note = node.applied_at
        ? t(`${S}node_note.applied`, { rules: node.applied_rules, ago: formatRelativeTimeShort(now - node.applied_at, t) })
        : t(`${S}node_note.applied_no_time`, { rules: node.applied_rules })
      break
    case 'applied_sniffing':
      note = t(`${S}node_note.applied_sniffing`, { n: node.sniffing_insufficient.length })
      break
    case 'sniffing':
      note = t(`${S}node_note.sniffing`, { inbounds: node.sniffing_insufficient.map(s => s.label).join(t(`${S}node_problem.list_separator`)) })
      break
    case 'over_limit':
      note = t(`${S}node_note.over_limit`, {
        what: t(`${S}verdict.what.${limitKindKey(node.over_limit?.kind)}`), used: num(node.over_limit?.used), limit: num(node.over_limit?.limit),
      })
      break
    default:
      note = t(`${S}node_note.${key}`)
  }
  // The enforced rules are untouched either way; this says only that the report-only ones are not running on the node.
  const reportNote = node.report_only_not_deployed ? t(`${S}node_note.report_not_deployed_${node.report_only_not_deployed}`) : null
  return { key, label, note, reportNote }
}

/**
 * The recording line of a coverage row (plan S14): what the node is set to
 * record, its hits of the last 24 hours and when it last reported. Only for a
 * node that can be set: a sing-box node's note already says it only enforces,
 * an old node's that it must be upgraded, and 3X-UI has none. A count the
 * server does not have (null) is left out, never written as 0, and a node
 * that does not record has no count to give.
 */
export function nodeRecordingText(t: T, node: DestNodeStatus, now: number): string | null {
  const rec = nodeRecording(node)
  if (!rec || (rec.mode !== 'off' && rec.mode !== 'hits' && rec.mode !== 'hits_and_usage')) return null
  const parts = [t(`${S}node_recording.mode.${rec.mode}`)]
  if ((rec.mode === 'hits' || rec.mode === 'hits_and_usage') && rec.hits24h !== null) parts.push(t(`${S}node_recording.hits_24h`, { n: new Intl.NumberFormat().format(rec.hits24h) }))
  if (node.last_report_at) parts.push(t(`${S}node_recording.reported`, { ago: formatRelativeTimeShort(now - node.last_report_at, t) }))
  return parts.join(t(`${S}node_recording.separator`))
}

/**
 * The hostname-matching line of a row: shown only where it says something, a
 * node asked for on_demand. As-is is the default and needs no line. The mode
 * named is the one the node was sent; asked-for but not yet delivered says so.
 */
export function nodeResolveText(t: T, node: DestNodeStatus): string | null {
  const v = nodeResolve(node)
  if (!v || (v.mode !== 'on_demand' && !v.unavailable)) return null
  const pending = v.mode === 'on_demand' && v.effective === 'as_is' && !v.unavailable
  return t(`${S}node_resolve.line`, { mode: t(pending ? `${S}node_resolve.pending` : `${S}node_resolve.mode.${v.effective}`) })
}

/**
 * A status badge with a test hook: the quiet kinds draw as a dot with no element of their own to hang
 * a test id on, so the id and the state ride on a wrapper.
 */
function StateBadge({ kind, label, testId, state }: { kind: AccessToneKind; label: string; testId: string; state: string }) {
  return <span data-testid={testId} data-state={state} className="inline-flex max-w-full"><AccessBadge kind={kind} label={label} /></span>
}

/**
 * Why a node set to on_demand runs as as_is: a quiet-amber badge with its words.
 * "No IP rules" is not a fault (nothing to resolve for), so it is a neutral note.
 */
export function ResolveUnavailableBadge({ reason }: { reason: ResolveUnavailable }) {
  const { t } = useTranslation(['admin'])
  return <StateBadge kind={reason === 'no_ip_rules' ? 'none' : 'attention'} label={t(`${S}node_resolve.unavailable.${reason}`)} testId="node-resolve-unavailable" state={reason} />
}

export interface NodePolicyStatusRowProps {
  node: DestNodeStatus
  variant: 'row' | 'block'
  now?: number
  onOpenInbound?: (nodeId: number) => void
  onRetry?: (agentId: string) => Promise<void> | void
  onUpgrade?: () => void
  onOpenLists?: () => void
  onOpenServer?: () => void
  /** Extra lines under the note (the coverage sheet's rule-data and GeoIP notes). */
  extra?: ReactNode
}

/** One supporting line under a node's state: muted by default, the foreground colour for the ones that need reading. */
function Line({ children, testId, strong }: { children: ReactNode; testId?: string; strong?: boolean }) {
  return <p data-testid={testId} className={strong ? 'text-sm text-foreground' : 'text-sm text-muted-foreground'}>{children}</p>
}

export default function NodePolicyStatusRow({
  node, variant, now = Date.now(), onOpenInbound, onRetry, onUpgrade, onOpenLists, onOpenServer, extra,
}: NodePolicyStatusRowProps) {
  const { t } = useTranslation(['admin'])
  const { key, label, note, reportNote } = nodeStateText(t, node, now)
  const allowLine = node.sniffing_allow_exempt_lost && node.sniffing_insufficient.length > 0 && <Line testId="node-sniffing-allow-lost">{t(`${S}node_note.sniffing_allow_lost`)}</Line>
  const reportLine = reportNote && <Line testId="node-report-not-deployed">{reportNote}</Line>
  const sepG = t(`${S}node_note.groups_separator`)
  const appliedGroups = node.allowlist_groups ?? []
  const droppedGroups = node.sniffing_groups_dropped ?? []
  const groupLines = (
    <>
      {appliedGroups.length > 0 && <Line testId="node-allowlist-groups">{t(`${S}node_note.allowlist_groups`, { groups: appliedGroups.map(g => g.name).join(sepG) })}</Line>}
      {droppedGroups.length > 0 && <Line testId="node-groups-dropped" strong>{t(`${S}node_note.groups_dropped`, { groups: droppedGroups.map(g => g.name).join(sepG) })}</Line>}
    </>
  )
  const badge = <StateBadge kind={NODE_STATE_TONE[key]} label={label} testId="node-state-badge" state={key} />
  const first = node.sniffing_insufficient[0]
  const recording = nodeRecordingText(t, node, now)
  const usageLine = node.usage_refused && <Line testId="node-usage-refused" strong>{t(`${S}node_note.usage_refused`)}</Line>
  const resolveText = nodeResolveText(t, node)
  const resolveView = nodeResolve(node)

  const actions: ReactNode[] = []
  const link = (id: string, text: string, onClick: () => void) => (
    <Button key={id} size="sm" variant="ghost" onClick={onClick}>{text}<IconChevronRight data-icon="inline-end" /></Button>
  )
  if (key === 'rejected' && onRetry) {
    actions.push(<AsyncButton key="retry" size="sm" variant="outline" onClick={() => onRetry(node.agent_id)}>{t(`${S}node_action.retry`)}</AsyncButton>)
  }
  if (node.usage_refused && key !== 'rejected' && onRetry) {
    actions.push(<AsyncButton key="usage-retry" size="sm" variant="outline" onClick={() => onRetry(node.agent_id)}>{t(`${S}node_action.retry`)}</AsyncButton>)
  }
  if ((key === 'sniffing' || key === 'applied_sniffing') && first && onOpenInbound) actions.push(link('inbound', t(`${S}node_action.open_inbound`), () => onOpenInbound(first.node_id)))
  if (key === 'over_limit' && onOpenLists) actions.push(link('lists', t(`${S}node_action.view_lists`), onOpenLists))
  if (key === 'unsupported_version' && onUpgrade) actions.push(link('upgrade', t(`${S}node_action.upgrade`), onUpgrade))
  if (key === 'unsupported_version' && !onUpgrade && onOpenServer) actions.push(link('server', t(`${S}node_action.open_server`), onOpenServer))

  if (variant === 'block') {
    // Title → impact → action, the way diagnostics/FindingList writes a problem.
    let problem: { title: string; impact: string } | null = null
    if (key === 'sniffing') problem = { title: t(`${S}node_problem.sniffing_title`, { inbounds: node.sniffing_insufficient.map(s => s.label).join(t(`${S}node_problem.list_separator`)) }), impact: t(`${S}node_problem.sniffing_impact`) }
    else if (key === 'applied_sniffing') problem = { title: t(`${S}node_problem.sniffing_partial_title`, { inbounds: node.sniffing_insufficient.map(s => s.label).join(t(`${S}node_problem.list_separator`)) }), impact: t(`${S}node_problem.sniffing_partial_impact`) }
    else if (key === 'rejected') problem = { title: t(`${S}node_problem.rejected_title`), impact: t(`${S}node_problem.rejected_impact`) }
    else if (key === 'over_limit') {
      problem = {
        title: t(`${S}node_problem.over_limit_title`, {
          what: t(`${S}verdict.what.${limitKindKey(node.over_limit?.kind)}`), used: node.over_limit?.used ?? 0, limit: node.over_limit?.limit ?? 0,
        }),
        impact: t(`${S}node_problem.over_limit_impact`),
      }
    }
    // The node's refusal of the usage setting is a problem of its own, but never hides a worse one.
    if (!problem && node.usage_refused) problem = { title: t(`${S}node_problem.usage_refused_title`), impact: t(`${S}node_problem.usage_refused_impact`) }
    return (
      <div data-testid="node-policy-block" className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">{badge}</div>
        <p className="mt-1.5 text-sm text-muted-foreground">{note}</p>
        {allowLine}
        {reportLine}
        {groupLines}
        {extra}
        {problem && (
          <div className="mt-3 rounded-lg bg-muted p-3">
            <p className="text-sm font-semibold">{problem.title}</p>
            <p className="text-sm text-muted-foreground">{problem.impact}</p>
          </div>
        )}
        {actions.length > 0 && <div className="mt-2 flex flex-wrap gap-2">{actions}</div>}
      </div>
    )
  }

  return (
    <div data-testid="node-policy-row" className="min-w-0 space-y-0.5 py-2.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="min-w-0 font-semibold [overflow-wrap:anywhere]">{node.panel_name}</span>
        {node.engine && <span className="text-xs text-muted-foreground">{node.engine}</span>}
        <span className="flex-1" />
        {badge}
      </div>
      <p className="text-sm text-muted-foreground">{note}</p>
      {allowLine}
      {reportLine}
      {groupLines}
      {usageLine}
      {recording && <Line testId="node-recording">{recording}</Line>}
      {resolveText && (
        <div className="flex flex-wrap items-center gap-2">
          <Line testId="node-resolve">{resolveText}</Line>
          {resolveView?.unavailable && <ResolveUnavailableBadge reason={resolveView.unavailable} />}
        </div>
      )}
      {extra}
      {actions.length > 0 && <div className="flex flex-wrap justify-end gap-2">{actions}</div>}
    </div>
  )
}
