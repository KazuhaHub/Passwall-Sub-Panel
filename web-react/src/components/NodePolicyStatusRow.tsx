import type { ReactNode } from 'react'
import { Box, Button, Typography, useTheme } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import ToneBadge from '@/components/ToneBadge'
import {
  accessTone, limitKindKey, nodeRecording, nodeStateKey, NODE_STATE_TONE, type NodeStateKey,
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
  if (!rec || (rec.mode !== 'off' && rec.mode !== 'hits')) return null
  const parts = [t(`${S}node_recording.mode.${rec.mode}`)]
  if (rec.mode === 'hits' && rec.hits24h !== null) parts.push(t(`${S}node_recording.hits_24h`, { n: new Intl.NumberFormat().format(rec.hits24h) }))
  if (node.last_report_at) parts.push(t(`${S}node_recording.reported`, { ago: formatRelativeTimeShort(now - node.last_report_at, t) }))
  return parts.join(t(`${S}node_recording.separator`))
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

export default function NodePolicyStatusRow({
  node, variant, now = Date.now(), onOpenInbound, onRetry, onUpgrade, onOpenLists, onOpenServer, extra,
}: NodePolicyStatusRowProps) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const { key, label, note, reportNote } = nodeStateText(t, node, now)
  const allowLine = node.sniffing_allow_exempt_lost && node.sniffing_insufficient.length > 0 && <Typography variant="body2" data-testid="node-sniffing-allow-lost" sx={{ color: md.onSurfaceVariant }}>{t(`${S}node_note.sniffing_allow_lost`)}</Typography>
  const reportLine = reportNote && <Typography variant="body2" data-testid="node-report-not-deployed" sx={{ color: md.onSurfaceVariant }}>{reportNote}</Typography>
  const badge = <ToneBadge tone={accessTone(theme, NODE_STATE_TONE[key])} label={label} testId="node-state-badge" data={key} />
  const first = node.sniffing_insufficient[0]
  const recording = nodeRecordingText(t, node, now)

  const actions: ReactNode[] = []
  const link = (id: string, text: string, onClick: () => void) => (
    <Button key={id} size="small" endIcon={<ChevronRightIcon />} onClick={onClick}>{text}</Button>
  )
  if (key === 'rejected' && onRetry) {
    actions.push(<AsyncButton key="retry" size="small" variant="outlined" onClick={() => onRetry(node.agent_id)}>{t(`${S}node_action.retry`)}</AsyncButton>)
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
    return (
      <Box data-testid="node-policy-block" sx={{ minWidth: 0 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>{badge}</Box>
        <Typography variant="body2" sx={{ mt: 0.75, color: md.onSurfaceVariant }}>{note}</Typography>
        {allowLine}
        {reportLine}
        {extra}
        {problem && (
          <Box sx={{ mt: 1.5, p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
            <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{problem.title}</Typography>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{problem.impact}</Typography>
          </Box>
        )}
        {actions.length > 0 && <Box sx={{ mt: 1, display: 'flex', gap: 1, flexWrap: 'wrap' }}>{actions}</Box>}
      </Box>
    )
  }

  return (
    <Box data-testid="node-policy-row" sx={{ minWidth: 0, py: 1.25 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography sx={{ fontWeight: 600, minWidth: 0, overflowWrap: 'anywhere' }}>{node.panel_name}</Typography>
        {node.engine && <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{node.engine}</Typography>}
        <Box sx={{ flex: 1 }} />
        {badge}
      </Box>
      <Typography variant="body2" sx={{ mt: 0.25, color: md.onSurfaceVariant }}>{note}</Typography>
      {allowLine}
      {reportLine}
      {recording && <Typography variant="body2" data-testid="node-recording" sx={{ color: md.onSurfaceVariant }}>{recording}</Typography>}
      {extra}
      {actions.length > 0 && <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', justifyContent: 'flex-end' }}>{actions}</Box>}
    </Box>
  )
}
