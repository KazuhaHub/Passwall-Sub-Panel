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
/** Batch 1: off | on. `report` is a later batch and the server refuses it. */
export type DestState = 'off' | 'on'
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
  /** Null until hit recording exists (batch 2). */
  hits_recent: number | null
  last_hit_at: number | null
}

export interface DestPolicies {
  allow: DestPolicy[]
  block: DestPolicy[]
  observe: DestPolicy[]
  exemptions: { count: number }
  allowlist_groups: unknown[]
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

export interface DestPolicyRef { kind: 'policy' | 'group'; id: number; name: string }

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

export interface DestTestInput {
  target: string
  port?: number
  network?: 'tcp' | 'udp'
  user_id?: number
  panel_id?: number
  inbound_node_id?: number
  source_ip?: string
}

export type DestTestStepKind = 'source' | 'allow' | 'block' | 'exempt' | 'builtin'
export type DestTestStepResult = 'hit' | 'miss' | 'untestable' | 'shadowed' | 'skipped'

export interface DestTestStep {
  step: number
  rule_id?: string
  kind: DestTestStepKind
  policy_id?: number
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
  /** Currently only hostname_skips_ip_rules. */
  notes: string[]
  builtin?: string
  conditional: boolean
  /** Saved changes that are not published yet: the result is of the published version. */
  unpublished: boolean
  nodes: { panel_id: number; name: string; state: string }[]
}

// ---- one account ----

export interface DestUserView {
  user_id: number
  upn: string
  exemption: DestExemption | null
  group: { id: number; name: string; mode: 'open' | string; stage: string | null } | null
  hits_available: boolean | null
  recent_hits: unknown
  usage_available: boolean | null
  usage_nodes: unknown
}

// ---- status ----

export type DestNodeState =
  | 'none' | 'paused' | 'unsupported_kind' | 'unsupported_version' | 'pending' | 'applied'
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
  collect: string
  collect_effective: string
  collecting: boolean
  state: DestNodeState
  fallback_reason: string
  over_limit: DestPublishError | null
  sniffing_insufficient: DestSniffView[]
  minted_at: number | null
  pending_since: number | null
  applied_at: number | null
  applied_rules: number
  last_report_at: number | null
  rules_data_version: string | null
  geoip_unavailable: boolean
  allowlist_groups: number[]
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

// ---- settings ----

export interface DestSettings {
  dest_policy_apply_min_seconds: number
  dest_list_refresh_hours: number
  dest_rules_update_mode: '' | 'manual' | 'auto'
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
  return { ...n, sniffing_insufficient: arr(n.sniffing_insufficient), allowlist_groups: arr(n.allowlist_groups) }
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
  return data
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

export async function updateDestList(id: number, input: DestListInput): Promise<DestListWrite> {
  const { data } = await client.put<DestListWrite>(`${BASE}/lists/${id}`, input, quiet)
  return normWrite(data)
}

export async function deleteDestList(id: number): Promise<void> {
  await client.delete(`${BASE}/lists/${id}`, quiet)
}

export async function editDestListEntries(id: number, edit: { add?: string[]; remove?: string[] }): Promise<DestListWrite> {
  const { data } = await client.post<DestListWrite>(`${BASE}/lists/${id}/entries`, edit, quiet)
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

/** Allow a site (`site`: the domain and its subdomains) or one host everywhere. Global scope only in this build. */
export interface DestExceptionResult {
  entry: string
  /** Set the first time: the exceptions list and the allow policy over it were created. */
  created: { list_id: number; policy_id: number } | null
  list_id?: number
  policy_id?: number
}

export async function addDestException(input: { target: string; match: 'site' | 'host' }): Promise<DestExceptionResult> {
  const { data } = await client.post<DestExceptionResult>(`${BASE}/exceptions`, { ...input, scope: 'global' }, quiet)
  return data
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
