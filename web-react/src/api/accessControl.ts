import { isAxiosError } from 'axios'
import { client } from './client'
import type { ReadOptions } from './requestOptions'

// The access-control API (handler/admin_dest*.go, adminGroup only, prefix
// /api/admin/dest). The shapes are the server's wire contract as built in
// batch 1 (docs/access-control.md §6): times are UTC milliseconds except the
// geosite release metadata, which the server sends as RFC 3339 strings.
//
// Writes are silent (`_skipErrorToast`): a 400 names a field or a code the
// page puts where it belongs, and a 409 is a stale read the page explains. The
// page reports whatever is left in its own words (destErrorOf below).

export type DestAction = 'block' | 'allow'
/**
 * off | report | on. `report` (a block policy or a source rule that only
 * records what it WOULD have blocked) is refused for an allow policy
 * (400 dest_policy_state_unavailable).
 */
export type DestState = 'off' | 'report' | 'on'
export type DestScope = 'all' | 'groups' | 'panels'
export type DestListKind = 'custom' | 'remote' | 'geosite'
export type DestListSide = 'dest' | 'source'
/** ready | refreshing | failed | pending (never downloaded). */
export type DestListState = 'ready' | 'refreshing' | 'failed' | 'pending'

export interface DestInline {
  cidrs?: string[]
  ports?: string
  network?: string
  protocols?: string[]
  private?: boolean
}

export interface DestBudgetItem { used: number; limit: number }
/** What the policies use of each wire limit. */
export interface DestBudget {
  rules: DestBudgetItem
  domains: DestBudgetItem
  regexps: DestBudgetItem
  cidrs: DestBudgetItem
  source_cidrs: DestBudgetItem
  subjects: DestBudgetItem
  bytes: DestBudgetItem
}
export type DestBudgetKind = keyof DestBudget

/** How the compile treated a policy: ok, a list part skipped, or nothing left. */
export interface DestPolicyNote {
  state: 'ok' | 'lists_not_ready' | 'empty'
  not_ready_lists?: number[]
  empty_lists?: number[]
  geoip_unavailable?: boolean
}

export interface DestPolicy {
  id: number
  name: string
  description: string
  action: DestAction
  state: DestState
  list_ids: number[]
  inline: DestInline
  scope: DestScope
  group_ids: number[]
  panel_ids: number[]
  /** The server's order within an action. */
  priority: number
  /** The catalog template it was made from, or ''. */
  template_key: string
  created_at: number
  updated_at: number
  list_states: { id: number; state: 'ready' | 'not_ready' | 'empty' }[]
  note: DestPolicyNote | null
  /** A group or panel the policy is scoped to is gone. */
  scope_missing: boolean
  /** Connections it blocked in the hit window. Null for an allow policy (it records nothing) and while no node records: never 0 then. */
  hits_recent: number | null
  /** What a report-only policy WOULD have blocked in the window; null unless its state is `report`, and while no node records. */
  would_block_recent: number | null
  /** Its latest hit of any kind within the retention; null when there is none (or for an allow policy). */
  last_hit_at: number | null
  /** A block policy's real blocks count as a risk signal (report-only rows never do). Absent from an older server: false. */
  counts_as_risk?: boolean
}

/** A group whose allowlist is in force (state is never `off` here), as the policies read lists it. */
export interface DestPolicyGroup { group_id: number; name: string; state: DestState; state_days: number }

export interface DestPolicies {
  allow: DestPolicy[]
  block: DestPolicy[]
  observe: DestPolicy[]
  exemptions: { count: number }
  allowlist_groups: DestPolicyGroup[]
  hit_window_days: number
  budget: DestBudget
}

/** The body of a policy create / update / preview. */
export interface DestPolicyInput {
  name: string
  description?: string
  action: DestAction
  state: DestState
  list_ids: number[]
  inline: DestInline
  scope: DestScope
  group_ids: number[]
  panel_ids: number[]
  template_key?: string
  /** Block policies only. Left out, an edit keeps the stored value; an allow policy must not send true. */
  counts_as_risk?: boolean
  /** A preview of an edit names the policy. */
  id?: number
  /** The precondition of a PUT. */
  updated_at?: number
}

export interface DestParseSample { line: number; text: string; reason: string }
export interface DestParseWarning { line: number; text: string; code: string }
export interface DestParseReport {
  accepted: number
  ignored: number
  rewritten: number
  duplicates: number
  samples: DestParseSample[]
  warnings: DestParseWarning[]
  warning_count: number
}

export interface DestList {
  id: number
  name: string
  kind: DestListKind
  side: DestListSide
  source_url?: string
  geosite_category?: string
  geosite_attrs?: string[]
  entry_count: number
  regexp_count: number
  state: DestListState
  empty: boolean
  last_fetched_at: number | null
  last_error: string
  /** Non-zero: the list belongs to a group's allowlist (a later batch). */
  owner_group_id: number
  updated_at: number
  used_by: DestPolicyRef[]
}

/** Who uses a list: a policy, an allowlist group, or a node's route by destination (`id` is the node, `name` its name). */
export interface DestPolicyRef { kind: 'policy' | 'group' | 'route'; id: number; name: string }

export interface DestListsView { items: DestList[]; refresh_hours: number; budget: DestBudget }

export interface DestListDetail { list: DestList; samples: string[]; source_text?: string }

/** The body of a list create / update / preview. */
export interface DestListInput {
  name?: string
  kind: DestListKind
  side?: DestListSide
  source_url?: string
  text?: string
  geosite_category?: string
  geosite_attrs?: string[]
  updated_at?: number
}

export interface DestListPreview {
  report: DestParseReport | null
  entries: string[]
  entry_count: number
  regexp_count: number
  /** A remote preview only. */
  http_status?: number
  bytes?: number
}

export interface DestListWrite { list: DestList; report: DestParseReport | null }

// ---- geosite ----

export interface GeositeMeta {
  tag: string
  sha256: string
  downloaded_at: string
  bytes: number
}

export interface GeositeLatest { tag: string; released_at?: string; checked_at?: string; error?: string }

export interface GeositeStatus {
  current?: GeositeMeta
  previous?: GeositeMeta
  current_released_at?: string
  current_age_days?: number
  latest: GeositeLatest
  update_available: boolean
  behind_days?: number
  mode: 'manual' | 'auto'
  data_error?: string
  last_update_error?: string
}

export interface GeositeCategory { name: string; entries: number; attrs: { name: string; count: number }[] }
export interface GeositeCategories { categories: GeositeCategory[]; updated_at?: number; release?: string }

export interface GeositeCategoryDiff {
  name: string
  old: number
  new: number
  added: number
  removed: number
  state: 'added' | 'removed' | 'changed'
  in_use: boolean
}

/** Why an update or a rollback cannot be applied; the old data stays. */
export interface GeositeRejection {
  reason: 'broad_entry' | 'dest_list_too_large' | 'category_gone' | 'policy_over_limit'
  list?: string
  list_id?: number
  entry?: string
  kind?: string
  used?: number
  limit?: number
}

export interface GeositeUpdateResult {
  dry_run: boolean
  from?: GeositeMeta
  to: GeositeMeta
  unchanged: boolean
  categories: GeositeCategoryDiff[]
  rejection?: GeositeRejection
  applied: boolean
  lists_changed: number
  bumped: boolean
}

// ---- catalog ----

export type Localized = Record<string, string>

export interface DestTemplate {
  key: string
  name: Localized
  description: Localized
  geosite?: string[]
  protocols?: string[]
  ports?: string
  network?: string
  private?: boolean
  added: boolean
  policy_id?: number
  policy_state?: DestState
  /** The policy it creates counts as a risk signal (its real blocks feed the dest_block signal). */
  counts_as_risk?: boolean
}

export interface DestUnavailableTemplate { key: string; name: Localized; reason: Localized }

export interface DestCatalog {
  catalog_version: number
  templates: DestTemplate[]
  unavailable: DestUnavailableTemplate[]
}

// ---- exemptions ----

export interface DestExemption {
  user_id: number
  upn: string
  reason: string
  created_by: number
  created_by_upn: string
  created_at: number
  expires_at: number | null
  /** Past its expiry and waiting for the hourly cleanup. */
  expired: boolean
}

export interface DestExemptionInput { user_id?: number; reason: string; expires_at: number | null }

// ---- source rules ----

export interface DestSourceRule {
  node_id: number
  countries: string[]
  cidrs: string[]
  state: DestState
  updated_at: number | null
  /** geoip_unavailable | empty: why a rule in force compiled to nothing. */
  note?: string
}

export interface DestSourceRuleView { rule: DestSourceRule; native: boolean; geoip_available: boolean }

export interface DestSourceRuleInput { countries: string[]; cidrs: string[]; state: DestState; updated_at?: number }

// ---- test ----

export type ResolveUnavailable = 'version' | 'route_only' | 'sniffing' | 'no_ip_rules'

export interface DestTestInput {
  target: string
  port?: number
  network?: 'tcp' | 'udp'
  user_id?: number
  panel_id?: number
  inbound_node_id?: number
  source_ip?: string
  /** The addresses the node would resolve the host name to (PSP does no DNS); at most 16 IP literals. Omitted: unknown. An empty array: the lookup failed. */
  resolved_ips?: string[]
}

export type DestTestStepKind = 'source' | 'allow' | 'block' | 'group' | 'exempt' | 'builtin'
export type DestTestStepResult = 'hit' | 'miss' | 'untestable' | 'shadowed' | 'skipped'

export interface DestTestStep {
  step: number
  rule_id?: string
  /** What the rule is; a report-only rule keeps its kind (see `report_only`). */
  kind: DestTestStepKind
  /** The rule is report-only (a policy or a source rule): a hit on it records and does not block. */
  report_only?: boolean
  policy_id?: number
  /** An allowlist group's step (g<gid>x1, g<gid>x2, g<gid>). */
  group_id?: number
  node_id?: number
  name?: string
  result: DestTestStepResult
  entry?: string
  builtin?: string
}

export interface DestTestResult {
  /** allow | block | default (no rule hit: the core's own routing). */
  verdict: 'allow' | 'block' | 'observe' | 'default'
  /** The rule id that ended it, "exempt", a builtin's name, or "". */
  terminating_step: string
  steps: DestTestStep[]
  /** hostname_skips_ip_rules, hostname_needs_resolution, resolution_failed_engine_dependent. */
  notes: string[]
  builtin?: string
  conditional: boolean
  /** Saved changes that are not published yet: the result is of the published version. */
  unpublished: boolean
  nodes: { panel_id: number; name: string; state: string }[]
}

// ---- hit records ----

/** Rows are one account's hits on one destination in one hour, or (group_by) a fold of them. */
export type DestHitAction = 'block' | 'observe'
export type DestHitSourceKind = 'policy' | 'source' | 'group'
export type DestHitGroupBy = 'none' | 'site' | 'user' | 'policy' | 'exit'

export interface DestHitItem {
  /** UTC ms of the start of the hour; 0 in a grouped view. */
  hour_ms: number
  panel_id: number
  panel_name: string | null
  /** The exit server of the tunnel the connection left through; 0: it left from the server itself. Absent in grouped views other than group_by=exit: not applicable, never "the server itself". */
  exit_panel_id?: number
  /** Null for the server itself, or an exit server that no longer exists. */
  exit_panel_name?: string | null
  user_id: number
  /** Null: the account is gone (or a grouped view that folds accounts away). */
  upn: string | null
  /** p<policy id>, s<inbound node id>, g<group id>; '' when a grouped view folds sources away. */
  source: string
  /** Null for a source that no longer exists: "a deleted policy". */
  source_name: string | null
  source_kind: DestHitSourceKind
  action: DestHitAction
  dest: string
  port: number
  count: number
  first_ms: number
  last_ms: number
}

export interface DestHitSourceRef { source: string; name: string | null }

export interface DestHits {
  items: DestHitItem[]
  total: number
  page: number
  page_size: number
  group_by: string
  since: number
  until: number
  /** The whole time / account / node selection, whatever the action and source filters. */
  summary: { block: number; deny: number; observe: number; users: number }
  /** Every source with rows in the range. */
  sources: DestHitSourceRef[]
  /** Reported rows that are missing from the range. */
  dropped_in_range: number
}

/** The query of GET /dest/hits. `since` and `until` are UTC ms (at most 31 days apart). `q` is a keyword and goes to the server only. */
export interface DestHitsParams {
  user_id?: number
  panel_id?: number
  /** Exit server: 0 selects what left from the server itself; left out, any. */
  exit_panel_id?: number
  source?: string
  source_kind?: DestHitSourceKind
  action?: DestHitAction
  since?: number
  until?: number
  q?: string
  group_by?: DestHitGroupBy
  page?: number
  page_size?: number
}

/** What a recent-hit line says: one source's hits of one account in the window. */
export interface DestUserHit {
  source: string
  source_name: string | null
  action: DestHitAction
  count: number
  /** The destinations hit most, at most three. */
  top_dests: string[] | null
  /** Panel ids the hits came from. */
  panels: number[] | null
  last_at: number
}

// ---- one account ----

export interface DestUserView {
  user_id: number
  upn: string
  exemption: DestExemption | null
  /** `state` is the group's allowlist state; `mode` and `stage` are the old fields, kept by the server for older pages. */
  group: { id: number; name: string; state?: DestState; state_changed_at?: number | null; mode?: 'open' | string; stage?: string | null } | null
  /** Whether any node of the account records hits; null when the server cannot tell. */
  hits_available: boolean | null
  /** Absent when the server has no hit store. `days` is the window, already clamped to the retention. */
  recent_hits: { days: number; items: DestUserHit[] } | null
  /** Whether at least one node the account has a client on records per-site usage now; when false, "no usage" says nothing. */
  usage_available: boolean | null
  usage_nodes: DestUsageNode[] | null
}

/** A node that records an account's per-site usage. */
export interface DestUsageNode { panel_id: number; name: string }

/** One site's connection count in an account's usage. */
export interface DestUsageSite { site: string; count: number }

/** What POST /dest/users/:id/usage answers: the twenty most-used sites and the rest as one line. */
export interface DestUsage {
  /** The window that was read: the requested days held to the usage retention. */
  effective_days: number
  nodes: DestUsageNode[]
  items: DestUsageSite[]
  others: { sites: number; count: number }
  total: number
  /** Usage rows the nodes or the panel could not keep in the window; the counts are at least this much short. */
  dropped_in_range: number
}

// ---- status ----

export type DestNodeState =
  | 'none' | 'paused' | 'unsupported_kind' | 'not_enrolled' | 'unsupported_version' | 'pending' | 'applied'
  | 'rejected' | 'over_limit' | 'sniffing' | 'offline'

export interface DestPublishError { kind: string; used?: number; limit?: number; field?: string }

export interface DestSniffView { listener: string; label: string; node_id: number; reason?: string }

export interface DestNodeStatus {
  panel_id: number
  agent_id: string
  panel_name: string
  /** psp | 3x-ui | s-ui, as the server names the panel kind. */
  kind: string
  engine: string
  agent_version: string
  supports: { policy: boolean; hits: boolean; usage: boolean }
  /** What the panel is set to record (off | hits | hits_and_usage); '' for a node that is not a Passwall Node. */
  collect: string
  /** What the node was told: the lower of the setting and what it can do. */
  collect_effective: string
  /** Whether it records now (derived; a quiet node sends nothing). */
  collecting: boolean
  /** Whether it records per-site usage now (derived like `collecting`). Absent from an older panel. */
  collecting_usage?: boolean
  /** The panel is set to per-site usage and the node refused the policy that records it: no usage is recorded until a retry, an upgrade or a capability change. */
  usage_refused?: boolean
  /** How the panel is set to treat host names under IP rules. Absent from an older panel (read as as_is). */
  resolve?: DestResolve
  /** What the node was actually sent; '' is as_is. */
  resolve_effective?: '' | 'on_demand'
  /** Why a node set to on_demand runs as_is. `no_ip_rules` is not a fault: the policy has nothing to resolve for. */
  resolve_unavailable?: ResolveUnavailable | null
  /** Why the node was sent the policy without its report-only rules (enforced ones untouched); null when it got them all. */
  report_only_not_deployed?: 'sniffing' | 'limit' | 'resolve' | null
  state: DestNodeState
  fallback_reason: string
  over_limit: DestPublishError | null
  sniffing_insufficient: DestSniffView[]
  /** An enforced domain allow rule is in force and does not apply on the listed inbounds. */
  sniffing_allow_exempt_lost?: boolean
  minted_at: number | null
  pending_since: number | null
  applied_at: number | null
  applied_rules: number
  last_report_at: number | null
  rules_data_version: string | null
  geoip_unavailable: boolean
  /** The allowlist groups in the policy this node last applied. */
  allowlist_groups: { id: number; name: string }[]
  /** Groups left out of this node's policy because an inbound's sniffing cannot read domains: they do not apply on any inbound of the node. */
  sniffing_groups_dropped?: { id: number; name: string }[]
  /** Hits it reported in the last 24 hours; null where the node cannot record. */
  hits_24h: number | null
}

export interface DestStatus {
  generation: number
  published_generation: number
  last_write_at: number | null
  next_publish_at: number | null
  apply_eta_ms: number
  paused: boolean
  publish_error: DestPublishError | null
  publish_error_at: number | null
  geosite_release: string
  geoip_unavailable: boolean
  totals: Record<string, number>
  nodes: DestNodeStatus[]
  policy_notes: Record<string, DestPolicyNote>
}

// ---- allowlist groups ----

export type DestGroupListState = 'ready' | 'not_ready' | 'empty'
export interface DestGroupList { id: number; name: string; entry_count: number; state: DestGroupListState }
export interface DestRef { id: number; name: string }
export interface DestPanelRef { panel_id: number; name: string }
/** Why a node does not enforce a group's allowlist. */
export type IneligibleReason = 'xui' | 'sui' | 'capability' | 'over_limit' | 'rejected' | 'sniffing'
export interface DestIneligible { panel_id: number; name: string; reason: IneligibleReason }
/** What the group's report-only rows say it would have denied. */
export interface DestWouldDeny { days: number; sites: number; count: number }
export interface DestSiteCount { site: string; count: number; added: boolean }

/** A group as the list shows it: `off` for a group with no allowlist (its `updated_at` is then null). */
export interface DestGroup {
  group_id: number
  group_name: string
  members: number
  state: DestState
  state_changed_at: number | null
  updated_at: number | null
  list_ids: number[]
  lists: DestGroupList[]
  base_list_id: number
  extra_list_id: number
  base_count: number
  eligible_nodes: number
  total_nodes: number
  ineligible: DestIneligible[]
  applied_nodes: number
  pending_nodes: DestPanelRef[]
  not_ready_lists: DestRef[]
  /** Null when the window has no report-only rows (not 0). */
  would_deny_recent: DestWouldDeny | null
  /** Null where nothing records. */
  denied_24h: number | null
}

export interface DestGroupReadiness {
  lists_ready: boolean
  not_ready_lists: DestRef[]
  unapplied_nodes: number
  base_nonempty: boolean
  remaining_sites_24h: number
  top_sites: DestSiteCount[]
}

export interface DestGroupDetail extends DestGroup { readiness: DestGroupReadiness }

export interface DestGroupPut {
  state: DestState
  list_ids: number[]
  /** A group's first allowlist (may be empty); an existing one must not send it. */
  base_hosts?: string[]
  /** An existing allowlist's precondition. */
  updated_at?: number
}

export interface DestGroupPreview {
  members: number
  eligible_nodes: number
  total_nodes: number
  ineligible: DestIneligible[]
  /** Nodes the group can use now and would lose by saving this state. */
  nodes_lost: DestIneligible[]
  /** Eligible nodes that enforce but do not record (sing-box, recording off). */
  non_recording_nodes: number
  not_ready_lists: DestRef[]
  base_hosts: string[]
  base_hosts_source: 'template' | 'none'
  would_deny_recent: (DestWouldDeny & { top: DestSiteCount[] }) | null
}

export interface DestGroupReportItem { site: string; count: number; last_at: number; added: boolean }

export interface DestGroupReport {
  effective_days: number
  state: DestState
  since_at: number | null
  items: DestGroupReportItem[]
  /** Reported rows lost on the nodes, summed over every group (a node counts drops by panel). */
  dropped_in_range: number
}

// ---- settings ----

export interface DestSettings {
  dest_policy_apply_min_seconds: number
  dest_list_refresh_hours: number
  dest_rules_update_mode: '' | 'manual' | 'auto'
  /** How long hit records are kept; 0 is unset (the default). */
  dest_hit_retention_days: number
  dest_trial_retention_days: number
  /** How long per-site usage is kept; 0 is unset (the default). Absent from an older panel. */
  dest_usage_retention_days?: number
}

export interface DestSettingsView {
  /** As stored: 0 and "" mean unset. */
  settings: DestSettings
  /** The shipped value of every numeric key. */
  defaults: Record<string, number>
  /** What the panel runs with. */
  effective: DestSettings
}

// ---- normalisation ----
// A Go nil slice is JSON null. Every array the page maps over is made [] here,
// once, so no screen has to guard it.

const arr = <T>(v: T[] | null | undefined): T[] => v ?? []

function normInline(i: DestInline | null | undefined): DestInline {
  const x = { ...(i ?? {}) }
  x.cidrs = arr(x.cidrs)
  x.protocols = arr(x.protocols)
  return x
}
function normPolicy(p: DestPolicy): DestPolicy {
  return { ...p, list_ids: arr(p.list_ids), group_ids: arr(p.group_ids), panel_ids: arr(p.panel_ids), list_states: arr(p.list_states), inline: normInline(p.inline) }
}
function normReport(r: DestParseReport | null | undefined): DestParseReport | null {
  return r ? { ...r, samples: arr(r.samples), warnings: arr(r.warnings) } : null
}
function normList(l: DestList): DestList {
  return { ...l, used_by: arr(l.used_by), geosite_attrs: arr(l.geosite_attrs) }
}
function normRule(r: DestSourceRule): DestSourceRule {
  return { ...r, countries: arr(r.countries), cidrs: arr(r.cidrs) }
}
function normNode(n: DestNodeStatus): DestNodeStatus {
  return { ...n, sniffing_insufficient: arr(n.sniffing_insufficient), allowlist_groups: arr(n.allowlist_groups), sniffing_groups_dropped: arr(n.sniffing_groups_dropped) }
}
function normGroup(g: DestGroup): DestGroup {
  return {
    ...g, list_ids: arr(g.list_ids), lists: arr(g.lists), ineligible: arr(g.ineligible), pending_nodes: arr(g.pending_nodes),
    not_ready_lists: arr(g.not_ready_lists), would_deny_recent: g.would_deny_recent ?? null, denied_24h: g.denied_24h ?? null,
  }
}
function normGroupDetail(g: DestGroupDetail): DestGroupDetail {
  const r = g.readiness
  return { ...normGroup(g), readiness: { ...r, not_ready_lists: arr(r?.not_ready_lists), top_sites: arr(r?.top_sites) } }
}
function normGeositeResult(r: GeositeUpdateResult): GeositeUpdateResult {
  return { ...r, categories: arr(r.categories) }
}
function normWrite(w: DestListWrite): DestListWrite {
  return { list: normList(w.list), report: normReport(w.report) }
}

// ---- reads ----

const BASE = '/admin/dest'

export async function getDestStatus(opts: ReadOptions = {}): Promise<DestStatus> {
  const { data } = await client.get<DestStatus>(`${BASE}/status`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, nodes: arr(data.nodes).map(normNode), policy_notes: data.policy_notes ?? {}, totals: data.totals ?? {} }
}

export async function getDestPolicies(opts: ReadOptions = {}): Promise<DestPolicies> {
  const { data } = await client.get<DestPolicies>(`${BASE}/policies`, { signal: opts.signal, _skipErrorToast: true })
  return {
    ...data, allow: arr(data.allow).map(normPolicy), block: arr(data.block).map(normPolicy),
    observe: arr(data.observe).map(normPolicy), allowlist_groups: arr(data.allowlist_groups),
  }
}

export async function getDestLists(opts: ReadOptions = {}): Promise<DestListsView> {
  const { data } = await client.get<DestListsView>(`${BASE}/lists`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, items: arr(data.items).map(normList) }
}

export async function getDestList(id: number, withText = false, opts: ReadOptions = {}): Promise<DestListDetail> {
  const { data } = await client.get<DestListDetail>(`${BASE}/lists/${id}`, {
    params: withText ? { text: 1 } : undefined, signal: opts.signal, _skipErrorToast: true,
  })
  return { ...data, list: normList(data.list), samples: arr(data.samples) }
}

export async function getDestCatalog(opts: ReadOptions = {}): Promise<DestCatalog> {
  const { data } = await client.get<DestCatalog>(`${BASE}/catalog`, { signal: opts.signal, _skipErrorToast: true })
  return {
    ...data, templates: arr(data.templates).map(t => ({ ...t, geosite: arr(t.geosite), protocols: arr(t.protocols) })),
    unavailable: arr(data.unavailable),
  }
}

export async function getDestExemptions(opts: ReadOptions = {}): Promise<DestExemption[]> {
  const { data } = await client.get<{ items: DestExemption[] }>(`${BASE}/exemptions`, { signal: opts.signal, _skipErrorToast: true })
  return arr(data.items)
}

export async function getDestSourceRules(opts: ReadOptions = {}): Promise<{ items: DestSourceRule[]; geoip_available: boolean }> {
  const { data } = await client.get<{ items: DestSourceRule[]; geoip_available: boolean }>(`${BASE}/source-rules`, {
    signal: opts.signal, _skipErrorToast: true,
  })
  return { items: arr(data.items).map(normRule), geoip_available: data.geoip_available === true }
}

export async function getDestSourceRule(nodeId: number, opts: ReadOptions = {}): Promise<DestSourceRuleView> {
  const { data } = await client.get<DestSourceRuleView>(`${BASE}/source-rules/${nodeId}`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, rule: normRule(data.rule) }
}

export async function getGeositeStatus(opts: ReadOptions = {}): Promise<GeositeStatus> {
  const { data } = await client.get<GeositeStatus>(`${BASE}/geosite/status`, { signal: opts.signal, _skipErrorToast: true })
  return data
}

export async function getGeositeCategories(opts: ReadOptions = {}): Promise<GeositeCategories> {
  const { data } = await client.get<GeositeCategories>(`${BASE}/geosite/categories`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, categories: arr(data.categories).map(c => ({ ...c, attrs: arr(c.attrs) })) }
}

export async function getDestUser(id: number, opts: ReadOptions = {}): Promise<DestUserView> {
  const { data } = await client.get<DestUserView>(`${BASE}/users/${id}`, { signal: opts.signal, _skipErrorToast: true })
  const hits = data.recent_hits
  return {
    ...data,
    recent_hits: hits ? { ...hits, items: arr(hits.items).map(h => ({ ...h, top_dests: arr(h.top_dests), panels: arr(h.panels) })) } : null,
    usage_nodes: data.usage_nodes ? arr(data.usage_nodes) : null,
  }
}

/**
 * One account's per-site usage over a range ("24h" or "<N>d"; the server holds N
 * to the usage retention). A POST on purpose: every read is a row in the admin
 * audit log, and nothing is cached.
 */
export async function postDestUserUsage(id: number, body: { range: string; panel_id?: number }, opts: ReadOptions = {}): Promise<DestUsage> {
  const { data } = await client.post<DestUsage>(`${BASE}/users/${id}/usage`, body, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, nodes: arr(data.nodes), items: arr(data.items), others: data.others ?? { sites: 0, count: 0 } }
}

/**
 * The hit records of a selection (plan S12). Parameters left out are not sent;
 * `q` is a keyword for the destination and travels in this request only: it is
 * never in the page URL (R12).
 */
export async function getDestHits(params: DestHitsParams = {}, opts: ReadOptions = {}): Promise<DestHits> {
  const query: Record<string, string | number> = {}
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '' || v === null) continue
    query[k] = v as string | number
  }
  const { data } = await client.get<DestHits>(`${BASE}/hits`, { params: query, signal: opts.signal, _skipErrorToast: true })
  return { ...data, items: arr(data.items), sources: arr(data.sources), summary: data.summary ?? { block: 0, deny: 0, observe: 0, users: 0 } }
}

export async function getDestGroups(opts: ReadOptions = {}): Promise<DestGroup[]> {
  const { data } = await client.get<{ items: DestGroup[] }>(`${BASE}/groups`, { signal: opts.signal, _skipErrorToast: true })
  return arr(data.items).map(normGroup)
}

export async function getDestGroup(id: number, opts: ReadOptions = {}): Promise<DestGroupDetail> {
  const { data } = await client.get<DestGroupDetail>(`${BASE}/groups/${id}`, { signal: opts.signal, _skipErrorToast: true })
  return normGroupDetail(data)
}

/** What a save would do, written nowhere. */
export async function previewDestGroup(id: number, input: { state: DestState; list_ids: number[] }, opts: ReadOptions = {}): Promise<DestGroupPreview> {
  const { data } = await client.get<DestGroupPreview>(`${BASE}/groups/${id}/preview`, {
    params: { state: input.state, list_ids: input.list_ids.join(',') }, signal: opts.signal, _skipErrorToast: true,
  })
  const w = data.would_deny_recent
  return {
    ...data, ineligible: arr(data.ineligible), nodes_lost: arr(data.nodes_lost), not_ready_lists: arr(data.not_ready_lists), base_hosts: arr(data.base_hosts),
    would_deny_recent: w ? { ...w, top: arr(w.top) } : null,
  }
}

/** The destinations a report-only group would have denied. `days` is clamped by the server to the retention. */
export async function getDestGroupReport(id: number, days?: number, opts: ReadOptions = {}): Promise<DestGroupReport> {
  const { data } = await client.get<DestGroupReport>(`${BASE}/groups/${id}/report`, {
    params: days ? { days } : {}, signal: opts.signal, _skipErrorToast: true,
  })
  return { ...data, items: arr(data.items) }
}

export async function getDestSettings(opts: ReadOptions = {}): Promise<DestSettingsView> {
  const { data } = await client.get<DestSettingsView>(`${BASE}/settings`, { signal: opts.signal, _skipErrorToast: true })
  return { ...data, defaults: data.defaults ?? {} }
}

// ---- writes ----

const quiet = { _skipErrorToast: true } as const

export async function previewDestPolicy(input: DestPolicyInput, opts: ReadOptions = {}): Promise<{ budget: DestBudget }> {
  const { data } = await client.post<{ budget: DestBudget }>(`${BASE}/policies/preview`, input, { signal: opts.signal, ...quiet })
  return data
}

export async function createDestPolicy(input: DestPolicyInput): Promise<DestPolicy> {
  const { data } = await client.post<{ policy: DestPolicy }>(`${BASE}/policies`, input, quiet)
  return normPolicy(data.policy)
}

/** The whole policy and the `updated_at` it was read at: the only way to change one (no PATCH). */
export async function updateDestPolicy(id: number, input: DestPolicyInput): Promise<DestPolicy> {
  const { data } = await client.put<{ policy: DestPolicy }>(`${BASE}/policies/${id}`, input, quiet)
  return normPolicy(data.policy)
}

export async function deleteDestPolicy(id: number): Promise<void> {
  await client.delete(`${BASE}/policies/${id}`, quiet)
}

/** The new order of ONE action's policies: exactly its current ids, or 409 dest_policy_order_stale. */
export async function reorderDestPolicies(action: DestAction, ids: number[]): Promise<void> {
  await client.put(`${BASE}/policies/order`, { action, ids }, quiet)
}

export async function previewDestList(input: DestListInput, opts: ReadOptions = {}): Promise<DestListPreview> {
  const { data } = await client.post<DestListPreview>(`${BASE}/lists/preview`, input, { signal: opts.signal, ...quiet })
  return { ...data, report: normReport(data.report), entries: arr(data.entries) }
}

export async function createDestList(input: DestListInput): Promise<DestListWrite> {
  const { data } = await client.post<DestListWrite>(`${BASE}/lists`, input, quiet)
  return normWrite(data)
}

/** `confirmRoutes`: the admin's go-ahead after a 409 dest_list_route_impact (the edit leaves a node's routes with nothing). */
const routesGoAhead = (confirmRoutes?: boolean) => (confirmRoutes ? { ...quiet, params: { confirm_routes: 'true' } } : quiet)

export async function updateDestList(id: number, input: DestListInput, confirmRoutes?: boolean): Promise<DestListWrite> {
  const { data } = await client.put<DestListWrite>(`${BASE}/lists/${id}`, input, routesGoAhead(confirmRoutes))
  return normWrite(data)
}

export async function deleteDestList(id: number): Promise<void> {
  await client.delete(`${BASE}/lists/${id}`, quiet)
}

export async function editDestListEntries(id: number, edit: { add?: string[]; remove?: string[] }, confirmRoutes?: boolean): Promise<DestListWrite> {
  const { data } = await client.post<DestListWrite>(`${BASE}/lists/${id}/entries`, edit, routesGoAhead(confirmRoutes))
  return normWrite(data)
}

/** 202: the refresh runs in the background; the list's state says when it ends. */
export async function refreshDestList(id: number): Promise<void> {
  await client.post(`${BASE}/lists/${id}/refresh`, undefined, quiet)
}

/** `dryRun` downloads and verifies a release and changes nothing; the apply names the tag the dry run reviewed. */
export async function updateGeosite(opts: { tag?: string; dryRun?: boolean } = {}): Promise<GeositeUpdateResult> {
  const { data } = await client.post<GeositeUpdateResult>(`${BASE}/geosite/update`, opts.tag ? { tag: opts.tag } : undefined, {
    params: opts.dryRun ? { dry_run: 1 } : undefined, timeout: 120_000, ...quiet,
  })
  return normGeositeResult(data)
}

export async function rollbackGeosite(dryRun = false): Promise<GeositeUpdateResult> {
  const { data } = await client.post<GeositeUpdateResult>(`${BASE}/geosite/rollback`, undefined, {
    params: dryRun ? { dry_run: 1 } : undefined, timeout: 120_000, ...quiet,
  })
  return normGeositeResult(data)
}

export async function instantiateDestTemplate(key: string, state: DestState, locale: string): Promise<DestPolicy> {
  const { data } = await client.post<{ policy: DestPolicy }>(`${BASE}/catalog/${encodeURIComponent(key)}/instantiate`, { state, locale }, quiet)
  return normPolicy(data.policy)
}

export async function createDestExemption(input: DestExemptionInput & { user_id: number }): Promise<DestExemption> {
  const { data } = await client.post<{ exemption: DestExemption }>(`${BASE}/exemptions`, input, quiet)
  return data.exemption
}

export async function updateDestExemption(userId: number, input: DestExemptionInput): Promise<DestExemption> {
  const { data } = await client.put<{ exemption: DestExemption }>(`${BASE}/exemptions/${userId}`, input, quiet)
  return data.exemption
}

export async function deleteDestExemption(userId: number): Promise<void> {
  await client.delete(`${BASE}/exemptions/${userId}`, quiet)
}

export async function putDestSourceRule(nodeId: number, input: DestSourceRuleInput): Promise<{ rule: DestSourceRule; geoip_available: boolean }> {
  const { data } = await client.put<{ rule: DestSourceRule; geoip_available: boolean }>(`${BASE}/source-rules/${nodeId}`, input, quiet)
  return { ...data, rule: normRule(data.rule) }
}

/** A POST, so it leaves an audit row (who tested what for whom). */
export async function testDestination(input: DestTestInput): Promise<DestTestResult> {
  const { data } = await client.post<DestTestResult>(`${BASE}/test`, input, quiet)
  return { ...data, steps: data.steps ?? [], notes: data.notes ?? [], nodes: data.nodes ?? [] }
}

/** From any state to any other. A group's first allowlist carries `base_hosts`; an existing one the `updated_at` it was read at (409 dest_group_stale). */
export async function putDestGroup(id: number, input: DestGroupPut): Promise<DestGroup> {
  const { data } = await client.put<DestGroup>(`${BASE}/groups/${id}`, input, quiet)
  return normGroup(data)
}

export async function publishDest(): Promise<{ published_generation: number; generation: number; unpublished: boolean }> {
  const { data } = await client.post<{ published_generation: number; generation: number; unpublished: boolean }>(`${BASE}/publish`, undefined, quiet)
  return data
}

export async function pauseDest(paused: boolean): Promise<void> {
  await client.put(`${BASE}/pause`, { paused }, quiet)
}

export async function retryDestAgent(agentId: string): Promise<{ retried: boolean }> {
  const { data } = await client.post<{ retried: boolean }>(`${BASE}/agents/${encodeURIComponent(agentId)}/retry`, undefined, quiet)
  return data
}

/** Only the keys that changed: the server merges them onto the stored record. */
export async function putDestSettings(changed: Partial<DestSettings>): Promise<DestSettingsView> {
  const { data } = await client.put<DestSettingsView>(`${BASE}/settings`, { settings: changed }, quiet)
  return { ...data, defaults: data.defaults ?? {} }
}

/** Allow a site (`site`: the domain and its subdomains) or one host, for everyone or for one allowlist group. */
export interface DestExceptionResult {
  entry: string
  /** Set the first time: the exceptions list and the allow policy over it were created. */
  created: { list_id: number; policy_id: number } | null
  list_id?: number
  policy_id?: number
}

export async function addDestException(input: { target: string; match: 'site' | 'host'; group_id?: number }): Promise<DestExceptionResult> {
  // With a group the entry goes to that group's supplement list (only its members); without, it is allowed for everyone.
  const scope = input.group_id ? { scope: 'group', group_id: input.group_id } : { scope: 'global' }
  const { data } = await client.post<DestExceptionResult>(`${BASE}/exceptions`, { target: input.target, match: input.match, ...scope }, quiet)
  return data
}

/** What a Passwall Node server records of its destination rules. `hits_and_usage` adds per-site connection counts (Xray nodes that declare it). */
export type AuditCollect = 'off' | 'hits' | 'hits_and_usage'

/** The recording mode of one native server: PUT /servers/:id with this one key (the others are kept). */
export async function setAuditCollect(serverId: number, collect: AuditCollect): Promise<void> {
  await client.put(`/admin/servers/${serverId}`, { audit_collect: collect }, quiet)
}

/**
 * How a Passwall Node server's IP-range and private-address rules meet a host name.
 * if_non_match is shown but never offered: the API refuses it.
 */
export type DestResolve = 'as_is' | 'on_demand'

/** The hostname-matching mode of one native server: PUT /servers/:id with this one key (the others are kept). */
export async function setDestResolve(serverId: number, resolve: DestResolve): Promise<void> {
  await client.put(`/admin/servers/${serverId}`, { dest_resolve: resolve }, quiet)
}

// ---- errors ----

/** What a refused request says, with the status and the code the page switches on. */
export interface DestError {
  status: number
  code: string
  /** The field a 400 names. */
  field?: string
  message: string
  /** Everything else in the body (lines, used_by, rejection, publish_error, kind/used/limit). */
  body: Record<string, unknown>
}

export function destErrorOf(err: unknown): DestError | null {
  if (!isAxiosError(err) || !err.response) return null
  const body = (err.response.data ?? {}) as Record<string, unknown>
  return {
    status: err.response.status,
    code: typeof body.code === 'string' ? body.code : '',
    field: typeof body.field === 'string' ? body.field : undefined,
    message: typeof body.error === 'string' ? body.error : err.message,
    body: typeof body === 'object' ? body : {},
  }
}

/** The 503 every route answers when the build has no access control. */
export function isDestUnavailable(err: unknown): boolean {
  return isAxiosError(err) && err.response?.status === 503 && (err.response.data as { code?: string } | undefined)?.code === 'dest_unavailable'
}
