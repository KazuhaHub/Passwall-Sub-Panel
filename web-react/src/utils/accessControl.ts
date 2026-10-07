import type { DestinationNodeStatus, DestinationPoliciesView, DestinationStatus } from '@/api/accessControl'
import type { StatusLineTone } from '@/components/StatusLine'
import type { Theme } from '@mui/material'
import { stateTone, type Tone } from '@/components/ToneBadge'
import { policyListAvailable } from './destinationListAvailability'
import BlockIcon from '@mui/icons-material/Block'
import LockOutlinedIcon from '@mui/icons-material/LockOutlined'
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined'
import ScienceOutlinedIcon from '@mui/icons-material/ScienceOutlined'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import VerifiedUserOutlinedIcon from '@mui/icons-material/VerifiedUserOutlined'

export type DestinationVerdictToneKind = 'block' | 'deny' | 'observe' | 'trial' | 'allow' | 'exempt' | 'direct' | 'untestable'
export function accessTone(theme: Theme, kind: DestinationVerdictToneKind): Tone {
  const md = theme.palette.md
  switch (kind) {
    case 'block': return { bg: md.surfaceContainerHighest, fg: md.error, Icon: BlockIcon }
    case 'deny': return { bg: md.surfaceContainerHighest, fg: md.error, Icon: LockOutlinedIcon }
    case 'observe': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: VisibilityOutlinedIcon }
    case 'trial': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: ScienceOutlinedIcon }
    case 'allow': return { bg: md.surfaceContainerHighest, fg: md.onSurfaceVariant, Icon: CheckCircleOutlineIcon }
    case 'exempt': return { bg: md.surfaceContainerHigh, fg: md.onSurface, Icon: VerifiedUserOutlinedIcon }
    case 'direct': return stateTone(theme, 'quiet')
    case 'untestable': return stateTone(theme, 'inhibited')
  }
}

export type AccessVerdictKind = 'paused' | 'publication' | 'rejected' | 'over_limit' | 'sniffing' | 'no_coverage' | 'upgrade' | 'offline' | 'list_failed' | 'list_pending' | 'unpublished' | 'pending' | 'applied' | 'quiet'
export interface AccessVerdict { kind: AccessVerdictKind; tone: StatusLineTone; done: number; total: number; count: number; name?: string }
export function accessVerdict(status: DestinationStatus, policies?: DestinationPoliciesView): AccessVerdict | null {
  if (!policies) return null
  const enabled = [...policies.allow, ...policies.block, ...policies.observe].filter(policy => policy.enabled)
  const native = status.nodes.filter(node => node.kind === 'psp' && node.supports.policy && node.state !== 'unsupported_version')
  const base = { done: native.filter(node => node.state === 'applied').length, total: native.length, count: enabled.length }
  const verdict = (kind: AccessVerdictKind, tone: StatusLineTone, count = base.count, name?: string): AccessVerdict => ({ ...base, kind, tone, count, name })
  if (status.paused) return verdict('paused', 'failing')
  if (status.publish_error) return verdict('publication', 'failing')
  for (const state of ['rejected', 'over_limit', 'sniffing'] as const) {
    const count = status.nodes.filter(node => node.state === state).length
    if (count) return verdict(state, 'failing', count)
  }
  const coverage = native.filter(node => node.state !== 'offline' && !node.fallback_exhausted).length
  if ((enabled.length || policies.allowlist_groups.length) && !coverage) return verdict('no_coverage', 'failing')
  for (const [state, kind] of [['unsupported_version', 'upgrade'], ['offline', 'offline']] as const) {
    const count = status.nodes.filter(node => node.kind === 'psp' && node.state === state).length
    if (count) return verdict(kind, 'attention', count)
  }
  const lists = enabled.flatMap(policy => policy.list_states)
  const failed = lists.find(list => list.state === 'failed')
  if (failed) return verdict('list_failed', 'attention', 1, failed.name)
  const pending = lists.find(list => !policyListAvailable(list) && ['pending', 'refreshing', 'missing', 'empty'].includes(list.state))
  if (pending) return verdict('list_pending', 'attention', 1, pending.name)
  if (status.generation !== status.published_generation) return verdict('unpublished', 'measuring')
  if (native.some(node => node.state === 'pending' || node.pending_since !== null)) return verdict('pending', 'measuring')
  if (!enabled.length && !policies.allowlist_groups.length) return verdict('quiet', 'quiet')
  return verdict('applied', 'ok', base.done)
}

export type NodeFilter = 'applied' | 'pending' | 'problem' | 'upgrade' | 'excluded'
export function nodeFilter(node: DestinationNodeStatus, now: number): NodeFilter {
  if (node.state === 'applied') return 'applied'
  if (node.state === 'pending') return node.pending_since !== null && now - node.pending_since > 600000 ? 'problem' : 'pending'
  if (['rejected', 'sniffing', 'over_limit'].includes(node.state)) return 'problem'
  if (node.state === 'unsupported_version') return 'upgrade'
  return 'excluded'
}
export function nodeAccessTone(node: DestinationNodeStatus, now: number): Parameters<typeof import('@/components/ToneBadge').stateTone>[1] {
  switch (node.state) {
    case 'applied': return 'ok'
    case 'pending': return nodeFilter(node, now) === 'problem' ? 'attention' : 'measuring'
    case 'rejected': case 'sniffing': case 'over_limit': return 'failing'
    case 'unsupported_version': return 'attention'
    case 'unsupported_kind': return 'not_applicable'
    case 'offline': return 'inhibited'
    case 'none': return 'none'
    case 'paused': return 'idle'
  }
}
export function fallbackState(node: DestinationNodeStatus): 'none' | 'waiting' | 'applied' | 'stopping' | 'exhausted' {
  const confirmed = node.minted_at !== null && node.pending_since === null && node.applied_at !== null
  if (node.fallback_exhausted) return confirmed && node.minted_kind === 'empty' ? 'exhausted' : 'stopping'
  if (node.minted_kind === 'fallback') return confirmed ? 'applied' : 'waiting'
  return 'none'
}
export function pipelineSteps(allowlist: boolean): Array<'allow' | 'exemption' | 'block' | 'group' | 'observe' | 'direct'> {
  return allowlist ? ['allow', 'exemption', 'block', 'group', 'observe', 'direct'] : ['allow', 'exemption', 'block', 'observe', 'direct']
}
export function statusNeedsPolling(status?: DestinationStatus): boolean {
  return !!status && (status.generation !== status.published_generation || status.nodes.some(node => node.state === 'pending' || node.pending_since !== null))
}
