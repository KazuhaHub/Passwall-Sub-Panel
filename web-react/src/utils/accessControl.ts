import type { ComponentType } from 'react'
import type { SvgIconProps, Theme } from '@mui/material'
import AllInclusiveIcon from '@mui/icons-material/AllInclusive'
import BlockIcon from '@mui/icons-material/Block'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import HelpOutlineIcon from '@mui/icons-material/HelpOutlineOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import LockOutlinedIcon from '@mui/icons-material/LockOutlined'
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutlineOutlined'
import RadioButtonUncheckedIcon from '@mui/icons-material/RadioButtonUnchecked'
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutlineOutlined'
import ScienceOutlinedIcon from '@mui/icons-material/ScienceOutlined'
import VerifiedUserOutlinedIcon from '@mui/icons-material/VerifiedUserOutlined'
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import type {
  DestAction, DestBudget, DestBudgetItem, DestListsView, DestNodeState, DestNodeStatus, DestPolicies, DestPolicy, DestState,
  DestStatus,
} from '@/api/accessControl'
import type { StatusLineTone } from '@/components/StatusLine'
import { amber, type Tone } from '@/components/ToneBadge'

// THE ACCESS-CONTROL PAGE'S PURE RULES: the closed colour table, the one
// node-state table, the page verdict and the small decisions several screens
// share. No React, no router, no i18n: every word is a key the views resolve,
// so a table-driven test can hold the rules without a renderer.

// ---------------------------------------------------------------------------
// The closed colour table (plan §7.2). One row per MEANING; a synonym shares
// its row, and no two rows share a (fill, words, icon) triple — the test
// asserts it. "Block" is a word, not an alarm: it is not drawn on red.
// ---------------------------------------------------------------------------

export const ACCESS_TONE_KINDS = [
  'block', 'allowlist_deny', 'observe', 'would_deny', 'allow', 'exempt', 'allowlist_enforced', 'ok', 'measuring',
  'attention', 'failing', 'paused', 'not_applicable', 'none', 'unknown', 'permanent',
] as const
export type AccessToneKind = (typeof ACCESS_TONE_KINDS)[number]

export function accessTone(theme: Theme, kind: AccessToneKind): Tone {
  const md = theme.palette.md
  const quiet = { bg: md.surfaceContainerHighest, fg: md.onSurfaceVariant }
  switch (kind) {
    case 'block': return { bg: md.surfaceContainerHighest, fg: md.error, Icon: BlockIcon }
    case 'allowlist_deny': return { bg: md.surfaceContainerHighest, fg: md.error, Icon: LockOutlinedIcon }
    case 'observe': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: VisibilityOutlinedIcon }
    case 'would_deny': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: ScienceOutlinedIcon }
    case 'allow': return { ...quiet, Icon: CheckCircleOutlineIcon }
    case 'exempt': return { bg: md.surfaceContainerHigh, fg: md.onSurface, Icon: VerifiedUserOutlinedIcon }
    case 'allowlist_enforced': return { bg: md.surfaceContainerHigh, fg: md.onSurface, Icon: LockOutlinedIcon }
    case 'ok': return { bg: md.surfaceContainerHigh, fg: theme.palette.success.main, text: md.onSurface, Icon: CheckCircleOutlineIcon }
    case 'measuring': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: HourglassEmptyIcon }
    case 'attention': return { ...amber(theme), text: md.onSurface, Icon: WarningAmberIcon }
    case 'failing': return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'paused': return { ...quiet, Icon: PauseCircleOutlineIcon }
    case 'not_applicable': return { ...quiet, Icon: RemoveCircleOutlineIcon }
    case 'none': return { ...quiet, Icon: RadioButtonUncheckedIcon }
    case 'unknown': return { ...quiet, Icon: HelpOutlineIcon }
    case 'permanent': return { ...quiet, Icon: AllInclusiveIcon }
  }
}

/** The icon component, for tests that compare the table's rows. */
export type ToneIcon = ComponentType<SvgIconProps>

// ---------------------------------------------------------------------------
// Node states (plan §7.4 S14): ONE table, read by the coverage sheet, the
// servers page and the single-node dialog (components/NodePolicyStatusRow).
// ---------------------------------------------------------------------------

/** A node that has been "applying" this long is a problem, not progress. */
export const PENDING_STUCK_MS = 10 * 60 * 1000

export type NodeStateKey =
  | 'applied' | 'applied_exec_only' | 'applied_sniffing' | 'pending' | 'pending_long' | 'rejected' | 'sniffing' | 'over_limit'
  | 'unsupported_version' | 'not_enrolled' | 'unsupported_kind' | 'offline' | 'none' | 'paused'

/** Filter chips of the coverage sheet (`node_state`). */
export const NODE_FILTERS = ['applied', 'pending', 'problem', 'upgrade', 'inactive'] as const
export type NodeFilter = (typeof NODE_FILTERS)[number]

export function parseNodeFilter(raw: string | null): NodeFilter | null {
  return (NODE_FILTERS as readonly string[]).includes(raw ?? '') ? raw as NodeFilter : null
}

export const NODE_STATE_TONE: Record<NodeStateKey, AccessToneKind> = {
  applied: 'ok',
  applied_exec_only: 'ok',
  // In effect, but some inbounds cannot sniff: domain and protocol rules do not run on them.
  applied_sniffing: 'attention',
  pending: 'measuring',
  pending_long: 'attention',
  rejected: 'failing',
  sniffing: 'failing',
  over_limit: 'failing',
  unsupported_version: 'attention',
  not_enrolled: 'unknown',
  unsupported_kind: 'not_applicable',
  offline: 'unknown',
  none: 'none',
  paused: 'paused',
}

/** Which filter chip each state is counted under. */
export const NODE_STATE_FILTER: Record<NodeStateKey, NodeFilter> = {
  applied: 'applied',
  applied_exec_only: 'applied',
  applied_sniffing: 'problem',
  pending: 'pending',
  pending_long: 'problem',
  rejected: 'problem',
  sniffing: 'problem',
  over_limit: 'problem',
  unsupported_version: 'upgrade',
  not_enrolled: 'inactive',
  unsupported_kind: 'inactive',
  offline: 'inactive',
  none: 'inactive',
  paused: 'inactive',
}

export function nodeStateKey(node: Pick<DestNodeStatus, 'state' | 'engine' | 'pending_since'> & Partial<Pick<DestNodeStatus, 'kind' | 'agent_id' | 'sniffing_insufficient'>>, now: number): NodeStateKey {
  switch (node.state as DestNodeState) {
    // The node runs what it can; inbounds whose sniffing cannot supply domain and protocol rules are still listed.
    case 'applied':
      if ((node.sniffing_insufficient?.length ?? 0) > 0) return 'applied_sniffing'
      return node.engine === 'sing-box' ? 'applied_exec_only' : 'applied'
    case 'pending':
      return node.pending_since && now - node.pending_since > PENDING_STUCK_MS ? 'pending_long' : 'pending'
    case 'rejected': return 'rejected'
    case 'sniffing': return 'sniffing'
    case 'over_limit': return 'over_limit'
    case 'not_enrolled': return 'not_enrolled'
    // A server before not_enrolled gave a native panel with no agent the same state
    // as an old node; the agent id it could not fill in tells them apart.
    case 'unsupported_version': return node.kind === 'psp' && node.agent_id === '' ? 'not_enrolled' : 'unsupported_version'
    case 'unsupported_kind': return 'unsupported_kind'
    case 'offline': return 'offline'
    case 'paused': return 'paused'
    default: return 'none'
  }
}

/** Whether a node is one this page sees as a native one: the 3X-UI and S-UI rows are "by design". */
export function isNativeKind(kind: string): boolean {
  return kind === 'psp'
}

/** The `{{what}}` of an over-limit or refused publish: the limit's kind as a key suffix. */
export function limitKindKey(kind: string | undefined): 'rules' | 'domains' | 'regexps' | 'cidrs' | 'bytes' | 'entries' | 'invalid' {
  switch (kind) {
    case 'rules': case 'domains': case 'regexps': case 'bytes': case 'cidrs': case 'entries': return kind
    case 'remote_bytes': return 'bytes'
    case 'source_cidrs': return 'cidrs'
    case 'subjects': return 'rules'
    default: return 'invalid'
  }
}

// ---------------------------------------------------------------------------
// The page verdict (plan §7.4 S1). The worst finding wins; 3X-UI and S-UI not
// executing is by design and never changes the colour.
// ---------------------------------------------------------------------------

export type VerdictKind =
  | 'paused' | 'publish_refused' | 'rejected' | 'sniffing' | 'over_limit' | 'no_coverage' | 'sniffing_partial'
  | 'upgrade' | 'offline' | 'list_failed' | 'list_pending'
  | 'waiting' | 'rolling' | 'rolling_slow' | 'applied' | 'idle'

export interface AccessVerdict {
  tone: StatusLineTone
  kind: VerdictKind
  /** What the title interpolates. */
  n: number
  done: number
  total: number
  /** A list's name (list_failed / list_pending). */
  name?: string
  /** The first node's name and the inbound labels behind a sniffing verdict. */
  node?: string
  inbounds?: string[]
  /** The publish error behind publish_refused, or the node's over-limit. */
  limit?: { kind: string; used?: number; limit?: number; field?: string }
  /** Which button the line offers. */
  action: 'nodes' | 'lists' | 'resume' | 'publish' | null
  /** The parts of "not enforced" the detail line lists; zero parts are left out. */
  skipped: { unsupportedKind: number; upgrade: number; offline: number; notEnrolled: number }
  /** Epoch ms the pending change goes out (waiting), for the countdown. */
  nextPublishAt: number | null
  /** How many nodes the pending change reaches. */
  executors: number
}

/**
 * How many nodes a first publish (or any apply) reaches: native nodes that can
 * run a policy and are neither offline nor too old. null when the status could
 * not be read ("each node", never 0). The ONE definition every confirmation and
 * footer line uses.
 */
export function enforcingNodeCount(status: Pick<DestStatus, 'nodes'> | undefined): number | null {
  if (!status) return null
  return status.nodes.filter(n => isNativeKind(n.kind) && n.supports.policy && n.state !== 'offline' && n.state !== 'unsupported_version').length
}

/**
 * Policies the nodes run: on, and report-only (which a node runs too, and
 * restarts for). Off is the only state that is not deployed.
 */
export function enabledPolicies(policies: Pick<DestPolicies, 'allow' | 'block' | 'observe'> | undefined): DestPolicy[] {
  if (!policies) return []
  return [...policies.allow, ...policies.block, ...policies.observe].filter(p => p.state !== 'off')
}

/** A native node that is online and runs a build that can enforce. */
function canEnforce(n: DestNodeStatus): boolean {
  return isNativeKind(n.kind) && n.state !== 'unsupported_kind' && n.state !== 'unsupported_version' && n.state !== 'not_enrolled' && n.state !== 'offline'
}

/**
 * The page's one sentence. `hasDefinitions` is true when anything enforceable
 * exists: an enabled policy or a source rule that is on (the status read does
 * not know source rules).
 */
export function accessVerdict(
  status: DestStatus,
  opts: { policies?: DestPolicies; lists?: DestListsView; hasDefinitions?: boolean; now: number },
): AccessVerdict {
  const now = opts.now
  const nodes = status.nodes
  const states = nodes.map(n => ({ n, key: nodeStateKey(n, now) }))
  const count = (pred: (k: NodeStateKey) => boolean) => states.filter(s => pred(s.key)).length
  const executors = nodes.filter(canEnforce)
  const enabled = enabledPolicies(opts.policies).length
  const hasDefinitions = opts.hasDefinitions ?? enabled > 0
  const skipped = {
    unsupportedKind: count(k => k === 'unsupported_kind'),
    upgrade: count(k => k === 'unsupported_version'),
    // A native panel with no agent yet: quiet, listed in the summary, never a colour.
    notEnrolled: count(k => k === 'not_enrolled'),
    offline: states.filter(s => s.key === 'offline' && isNativeKind(s.n.kind)).length,
  }
  const done = count(k => k === 'applied' || k === 'applied_exec_only' || k === 'applied_sniffing')
  const base: AccessVerdict = {
    tone: 'ok', kind: 'applied', n: done, done, total: executors.length, action: 'nodes', skipped,
    nextPublishAt: status.next_publish_at, executors: executors.length,
  }
  const first = (key: NodeStateKey) => states.find(s => s.key === key)

  if (status.paused) return { ...base, tone: 'failing', kind: 'paused', action: 'resume' }

  if (status.publish_error) {
    return { ...base, tone: 'failing', kind: 'publish_refused', limit: status.publish_error, action: 'lists' }
  }
  const rejected = first('rejected')
  if (rejected) return { ...base, tone: 'failing', kind: 'rejected', n: count(k => k === 'rejected'), node: rejected.n.panel_name }
  const sniffing = first('sniffing')
  if (sniffing) {
    return {
      ...base, tone: 'failing', kind: 'sniffing', n: count(k => k === 'sniffing'), node: sniffing.n.panel_name,
      inbounds: sniffing.n.sniffing_insufficient.map(s => s.label),
    }
  }
  const over = first('over_limit')
  if (over) {
    return { ...base, tone: 'failing', kind: 'over_limit', n: count(k => k === 'over_limit'), node: over.n.panel_name, limit: over.n.over_limit ?? undefined, action: 'lists' }
  }
  if (hasDefinitions && executors.length === 0) {
    return { ...base, tone: 'failing', kind: 'no_coverage', n: enabled }
  }

  // Applied, but some inbounds cannot run domain and protocol rules: attention, not failure.
  const partial = first('applied_sniffing')
  if (partial) {
    return {
      ...base, tone: 'attention', kind: 'sniffing_partial', n: count(k => k === 'applied_sniffing'), node: partial.n.panel_name,
      inbounds: partial.n.sniffing_insufficient.map(s => s.label), action: 'nodes',
    }
  }
  if (skipped.upgrade > 0) return { ...base, tone: 'attention', kind: 'upgrade', n: skipped.upgrade }
  if (skipped.offline > 0) return { ...base, tone: 'attention', kind: 'offline', n: skipped.offline }
  const listProblem = (opts.lists?.items ?? []).find(l =>
    l.state === 'failed' && l.last_fetched_at !== null && l.used_by.length > 0)
  if (listProblem) return { ...base, tone: 'attention', kind: 'list_failed', name: listProblem.name, action: 'lists' }
  const neverDownloaded = (opts.lists?.items ?? []).find(l =>
    (l.state === 'pending' || (l.state === 'failed' && l.last_fetched_at === null)) && l.used_by.some(u => isEnabledRef(opts.policies, u.id)))
  if (neverDownloaded) return { ...base, tone: 'attention', kind: 'list_pending', name: neverDownloaded.name, action: 'lists' }

  const unpublished = status.generation !== status.published_generation
  if (unpublished) {
    return { ...base, tone: 'measuring', kind: 'waiting', action: 'publish', n: executors.length }
  }
  const pending = count(k => k === 'pending' || k === 'pending_long')
  if (pending > 0) {
    const slow = count(k => k === 'pending_long') > 0
    return { ...base, tone: slow ? 'attention' : 'measuring', kind: slow ? 'rolling_slow' : 'rolling', total: done + pending }
  }
  if (!hasDefinitions) return { ...base, tone: 'quiet', kind: 'idle', action: null }
  return base
}

function isEnabledRef(policies: DestPolicies | undefined, id: number): boolean {
  return enabledPolicies(policies).some(p => p.id === id)
}

/** Whether the status read should be repeated: a node is applying, or a saved change waits to go out. */
export function destStatusActive(status: DestStatus | undefined): boolean {
  if (!status) return false
  if (status.paused) return false
  if (status.nodes.some(n => n.state === 'pending')) return true
  return status.generation !== status.published_generation && !status.publish_error
}

/** Whether a list read should be repeated: one is being refreshed. */
export function listsRefreshing(lists: DestListsView | undefined): boolean {
  return !!lists?.items.some(l => l.state === 'refreshing')
}

// ---------------------------------------------------------------------------
// Budget (plan §7.4 S2/S3): ≥ 80% is a warning, > 100% blocks the save.
// ---------------------------------------------------------------------------

export type BudgetLevel = 'ok' | 'near' | 'over'

export function budgetLevel(item: DestBudgetItem | undefined): BudgetLevel {
  if (!item || item.limit <= 0) return 'ok'
  if (item.used > item.limit) return 'over'
  return item.used / item.limit >= 0.8 ? 'near' : 'ok'
}

/** The four quotas always shown; `subjects` and `bytes` only when ≥ 80%. */
export function visibleBudget(b: DestBudget): (keyof DestBudget)[] {
  const always: (keyof DestBudget)[] = ['rules', 'domains', 'regexps', 'cidrs']
  const sometimes: (keyof DestBudget)[] = ['subjects', 'bytes']
  return [...always, ...sometimes.filter(k => budgetLevel(b[k]) !== 'ok')]
}

export function anyBudgetOver(b: DestBudget | undefined): boolean {
  return !!b && (Object.keys(b) as (keyof DestBudget)[]).some(k => budgetLevel(b[k]) === 'over')
}

// ---------------------------------------------------------------------------
// The match-order rail (plan §7.4 S2): numbering is computed, never written.
// ---------------------------------------------------------------------------

export type PipelineStepId = 'allow' | 'exempt' | 'block' | 'allowlist' | 'report' | 'rest'

/**
 * The steps that exist at this stage, numbered from 1. Report-only is the
 * step after every enforced one (the compiler places it there so that it can
 * never change a verdict); there are no allowlist groups yet, so the rail is
 * allow, exempt, block, report-only, rest — and the numbers move up by
 * themselves when a later batch turns a step on.
 */
export function pipelineSteps(features: { report?: boolean; allowlist?: boolean } = {}): { id: PipelineStepId; n: number }[] {
  const ids: PipelineStepId[] = ['allow', 'exempt', 'block']
  if (features.allowlist) ids.push('allowlist')
  if (features.report) ids.push('report')
  ids.push('rest')
  return ids.map((id, i) => ({ id, n: i + 1 }))
}

export const CIRCLED = ['①', '②', '③', '④', '⑤', '⑥', '⑦'] as const

// ---------------------------------------------------------------------------
// First publish (plan §7.4 S20): the one write that moves the whole fleet from
// "nothing enforced" to "something enforced" asks first. Every entry point
// goes through this one function.
// ---------------------------------------------------------------------------

export function needsFirstPublishConfirm(
  current: { policiesOn: number; sourceRulesOn: number },
  change: { turnsOn: boolean },
): boolean {
  return change.turnsOn && current.policiesOn === 0 && current.sourceRulesOn === 0
}

/** `ceil(apply_eta_ms / 60000)`; null when the server did not say (the copy then says "a few minutes"). */
export function applyMinutes(status: Pick<DestStatus, 'apply_eta_ms'> | undefined): number | null {
  const ms = status?.apply_eta_ms
  if (ms === undefined || ms === null || !(ms > 0)) return null
  return Math.max(1, Math.ceil(ms / 60000))
}

/** The window fixed statistics read over, clamped to the retention (R14). */
export function clampDays(fixed: number, retention: number | undefined): number {
  return retention && retention > 0 ? Math.min(fixed, retention) : fixed
}

// ---------------------------------------------------------------------------
// The three states (owner decision): Off / Report-only / On. The admin picks
// any of them freely; the UI may recommend report-only for a new block policy
// and never forces it. An allow policy has no report-only (a verdict that
// "would have been" an allow cannot be told from the real one).
// ---------------------------------------------------------------------------

export function policyStateOptions(action: DestAction): DestState[] {
  return action === 'allow' ? ['off', 'on'] : ['off', 'report', 'on']
}

/**
 * The badge a state adds to a row: off is the quiet "paused" row, report-only
 * the "would be denied" row (ScienceOutlined: what a policy WOULD do, the same
 * meaning the closed table already gives the trial), and on adds none (the
 * action badge is the whole story of an enforced policy).
 */
export function policyStateTone(state: DestState): AccessToneKind | null {
  switch (state) {
    case 'off': return 'paused'
    case 'report': return 'would_deny'
    default: return null
  }
}

/** The block section's rows by what the compiler does with them: report-only rows are step 4 of the rail, everything else step 3. */
export function splitBlockByState(block: DestPolicy[]): { enforced: DestPolicy[]; report: DestPolicy[] } {
  return { enforced: block.filter(p => p.state !== 'report'), report: block.filter(p => p.state === 'report') }
}

/**
 * The server's order of one action is ONE list, but the rail shows it as two
 * sections. A move inside a section rewrites only the slots that section holds
 * in the whole order, so the other section's rows keep theirs. null: the move
 * goes nowhere.
 */
export function reorderWithin(all: number[], section: number[], id: number, move: 'up' | 'down' | 'top' | 'bottom'): number[] | null {
  const i = section.indexOf(id)
  if (i < 0) return null
  const to = move === 'up' ? i - 1 : move === 'down' ? i + 1 : move === 'top' ? 0 : section.length - 1
  if (to < 0 || to >= section.length || to === i) return null
  const next = [...section]
  next.splice(i, 1)
  next.splice(to, 0, id)
  const slots = new Set(section)
  let k = 0
  return all.map(x => (slots.has(x) ? next[k++] : x))
}

// ---------------------------------------------------------------------------
// Hits (plan S2): a count is only as good as the recording behind it.
// ---------------------------------------------------------------------------

export type PolicyHitsView =
  | { kind: 'none' }
  | { kind: 'unavailable' }
  | { kind: 'count'; count: number; days: number; last: number | null }
  | { kind: 'zero'; days: number; last: number | null }
  | { kind: 'never' }
  | { kind: 'would_block'; count: number; days: number; last: number | null; notDeployed?: number }
  | { kind: 'would_block_none'; days: number; notDeployed?: number }

/**
 * What a policy row says about hits. null counts are "no node records" and
 * are NEVER drawn as 0; an allow policy records nothing and says nothing;
 * a report-only policy speaks of what it would have blocked.
 */
export function policyHitsView(p: DestPolicy, days: number, notDeployed = 0): PolicyHitsView {
  if (p.action === 'allow') return { kind: 'none' }
  if (p.state === 'report') {
    if (p.would_block_recent === null) return { kind: 'unavailable' }
    // Nodes that were not sent the report-only rules recorded nothing for them: a count (or a zero) is then of the others only.
    const shed = notDeployed > 0 ? { notDeployed } : {}
    return p.would_block_recent > 0
      ? { kind: 'would_block', count: p.would_block_recent, days, last: p.last_hit_at, ...shed }
      : { kind: 'would_block_none', days, ...shed }
  }
  if (p.hits_recent === null) return { kind: 'unavailable' }
  if (p.hits_recent > 0) return { kind: 'count', count: p.hits_recent, days, last: p.last_hit_at }
  return p.last_hit_at === null ? { kind: 'never' } : { kind: 'zero', days, last: p.last_hit_at }
}

/** Nodes the server sent the policy WITHOUT its report-only rules (sniffing, or a limit). */
export function reportNotDeployedCount(
  status: { nodes: Pick<DestNodeStatus, 'panel_id' | 'report_only_not_deployed'>[] } | undefined,
  policy?: Pick<DestPolicy, 'scope' | 'panel_ids' | 'group_ids'>,
): number {
  if (!status) return 0
  // A panel-scoped policy is sent only to its panels' nodes. Which nodes a
  // group's members reach is not in the status, so a group scope (and the
  // fleet-wide rail) counts every node that reports a shed: an upper bound
  // for those nodes.
  const inScope = policy?.scope === 'panels' ? (n: { panel_id: number }) => policy.panel_ids.includes(n.panel_id) : () => true
  return status.nodes.filter(n => !!n.report_only_not_deployed && inScope(n)).length
}

/** Native nodes that record hits now (the derived flag: a node that was only told to is not counted). */
export function collectingNodeCount(status: Pick<DestStatus, 'nodes'> | undefined): number {
  return status ? status.nodes.filter(n => n.collecting).length : 0
}

export type NodeRecordingMode = 'off' | 'hits' | 'too_old' | 'exec_only'

/**
 * A native node's recording line: what it is set to, or why it cannot record.
 * null for a node this page does not run policies on (3X-UI, S-UI).
 * `hits24h` is null where the node cannot record, never 0.
 */
export function nodeRecording(node: Pick<DestNodeStatus, 'kind' | 'state' | 'engine' | 'supports' | 'collect' | 'collect_effective' | 'hits_24h'>): { mode: NodeRecordingMode; hits24h: number | null } | null {
  if (!isNativeKind(node.kind) || node.state === 'unsupported_kind') return null
  if (node.engine === 'sing-box') return { mode: 'exec_only', hits24h: null }
  if (!node.supports.hits) return { mode: 'too_old', hits24h: null }
  // What the node was told is what it does; the stored choice only stands in before anything was delivered.
  const set = node.collect_effective || node.collect
  return { mode: set === 'off' ? 'off' : 'hits', hits24h: node.hits_24h }
}
