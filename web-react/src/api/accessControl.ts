import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { UISettings } from './settings'

export type DestinationTestStepName = 'allow' | 'exemption' | 'block' | 'group' | 'observe' | 'direct'
export type DestinationNodeState =
  | 'none' | 'paused' | 'unsupported_kind' | 'unsupported_version' | 'pending'
  | 'applied' | 'rejected' | 'over_limit' | 'sniffing' | 'offline'
export interface DestinationTestInput {
  target: string
  port?: number
  network?: 'tcp' | 'udp'
  user_id?: number
  panel_id?: number
}
export interface DestinationTestResult {
  verdict: 'allow' | 'block' | 'observe' | 'exempt' | 'direct' | 'untestable'
  terminating_step: DestinationTestStepName | null
  steps: Array<{
    step: DestinationTestStepName
    policy_id?: number
    group_id?: number
    name?: string
    result: 'miss' | 'n/a' | 'hit' | 'shadowed' | 'skipped' | 'untestable'
    list_id?: number
    entry?: string
  }>
  notes: string[]
  unpublished: boolean
  nodes: Array<{ panel_id: number; name: string; state: DestinationNodeState; execution?: DestinationTestExecution }>
}
export interface DestinationTestExecution {
  engine: string | null
  minted_kind: DestinationNodeStatus['minted_kind']
  fallback_exhausted: boolean
  minted_at: number | null
  applied_at: number | null
  pending_since: number | null
  applied_rules: number | null
}

/** Readonly simulation with write-audit capture; target stays in the POST body. */
export async function testDestination(input: DestinationTestInput, opts: ReadOptions = {}): Promise<DestinationTestResult> {
  const { data } = await client.post<DestinationTestResult>('/admin/dest/test', input, {
    signal: opts.signal, _skipErrorToast: true,
  })
  return data
}

export type AccessControlSettingKey =
  | 'dest_hit_retention_days' | 'dest_trial_retention_days' | 'dest_usage_retention_days'
  | 'dest_list_refresh_hours' | 'dest_policy_apply_min_seconds'

/** Stored zero selects the default returned by the server. */
export type AccessControlSettings = Required<Pick<UISettings, AccessControlSettingKey>>

export interface AccessControlSettingsView {
  settings: AccessControlSettings
  defaults: AccessControlSettings
  effective: AccessControlSettings
}

export async function getAccessControlSettings(opts: ReadOptions = {}): Promise<AccessControlSettingsView> {
  const { data } = await client.get<AccessControlSettingsView>('/admin/dest/settings', {
    signal: opts.signal,
    _skipErrorToast: opts.silent,
  })
  return data
}

/** Send only edited fields so another tab's changes remain intact. */
export async function putAccessControlSettings(changed: Partial<AccessControlSettings>): Promise<AccessControlSettingsView> {
  const { data } = await client.put<AccessControlSettingsView>('/admin/dest/settings', { settings: changed }, {
    _skipErrorToast: true,
  })
  return data
}

export interface DestinationPolicyRetryResult {
  retry_requested: boolean
}

export async function retryDestinationPolicy(agentId: string): Promise<DestinationPolicyRetryResult> {
  const { data } = await client.post<DestinationPolicyRetryResult>(
    `/admin/dest/agents/${encodeURIComponent(agentId)}/retry`, undefined, { _skipErrorToast: true },
  )
  return data
}

export type DestinationListKind = 'custom' | 'remote' | 'geosite'
export interface DestinationParseReport {
  accepted: number
  ignored: number
  ignored_broad: number
  rewritten: number
  samples: Array<{ line: number; text: string; reason: string; normalized?: string }>
}
export interface DestinationReference { kind: 'policy' | 'group'; id: number; name: string }
export interface DestinationListSummary {
  id: number
  name: string
  kind: DestinationListKind
  source_url: string
  geosite_category: string
  geosite_attrs: string
  entry_count: number
  regexp_count: number
  state: 'ready' | 'refreshing' | 'failed' | 'pending'
  last_fetched_at: number | null
  last_error: string
  owner_group_id: number
  updated_at: number
  parse_report_summary: Omit<DestinationParseReport, 'samples'> | null
  used_by: DestinationReference[]
}
export interface DestinationListDetail extends Omit<DestinationListSummary, 'parse_report_summary' | 'used_by'> {
  content_sha256: string
  entry_types?: Record<'domain' | 'full' | 'keyword' | 'regexp' | 'cidr', number>
  parse_report: DestinationParseReport | null
  entries: string[]
  source_text?: string
}
export type DestinationBudget = Record<'rules' | 'domains' | 'regexps' | 'cidrs' | 'subjects' | 'bytes', { used: number; limit: number }>
export interface DestinationListsView { items: DestinationListSummary[]; refresh_hours: number; budget: DestinationBudget }
export interface DestinationListInput {
  name: string
  kind: DestinationListKind
  source_url?: string
  geosite_category?: string
  geosite_attrs?: string
  text?: string
}
export interface DestinationListPreview {
  content_sha256: string
  parse_report: DestinationParseReport
  entries: string[]
  entry_count: number
  regexp_count: number
  http_status: number
  bytes: number
}
export interface DestinationCategoriesView {
  categories: Array<{ name: string; count: number; regexp_count: number; source_count: number; ignored_broad_count: number; attrs: string[] }>
  updated_at: number
  refreshing?: boolean
  last_error?: string
}

export async function getDestinationLists(opts: ReadOptions = {}): Promise<DestinationListsView> {
  const { data } = await client.get<DestinationListsView>('/admin/dest/lists', { signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export async function getDestinationList(id: number, text = false, opts: ReadOptions = {}): Promise<DestinationListDetail> {
  const { data } = await client.get<DestinationListDetail>(`/admin/dest/lists/${id}`, { params: text ? { text: 1 } : undefined, signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export async function previewDestinationList(input: DestinationListInput, signal?: AbortSignal): Promise<DestinationListPreview> {
  const { data } = await client.post<DestinationListPreview>('/admin/dest/lists/preview', input, { signal, _skipErrorToast: true })
  return data
}
export async function createDestinationList(input: DestinationListInput): Promise<DestinationListDetail> {
  const { data } = await client.post<DestinationListDetail>('/admin/dest/lists', input, { _skipErrorToast: true })
  return data
}
export async function putDestinationList(id: number, input: DestinationListInput & { updated_at: number }): Promise<DestinationListDetail> {
  const { data } = await client.put<DestinationListDetail>(`/admin/dest/lists/${id}`, input, { _skipErrorToast: true })
  return data
}
export async function deleteDestinationList(id: number): Promise<void> {
  await client.delete(`/admin/dest/lists/${id}`, { _skipErrorToast: true })
}
export async function patchDestinationListEntries(id: number, add: string[], remove?: string[]): Promise<DestinationListDetail> {
  const { data } = await client.post<DestinationListDetail>(`/admin/dest/lists/${id}/entries`, { add, remove }, { _skipErrorToast: true })
  return data
}
export async function refreshDestinationList(id: number): Promise<void> {
  await client.post(`/admin/dest/lists/${id}/refresh`, undefined, { _skipErrorToast: true })
}
export async function getDestinationCategories(opts: ReadOptions = {}): Promise<DestinationCategoriesView> {
  const { data } = await client.get<DestinationCategoriesView>('/admin/dest/geosite/categories', { signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export async function refreshDestinationCategories(): Promise<void> {
  await client.post('/admin/dest/geosite/refresh', undefined, { _skipErrorToast: true })
}

export type DestinationPolicyAction = 'allow' | 'block' | 'observe'
export interface DestinationPolicyInline {
  cidrs?: string[]
  ports?: string
  network?: '' | 'tcp' | 'udp'
  protocols?: string[]
  private?: boolean
}
export interface DestinationPolicyInput {
  name: string
  action: DestinationPolicyAction
  list_ids: number[]
  inline: DestinationPolicyInline
  scope: 'all' | 'groups'
  group_ids: number[]
  enabled: boolean
  counts_as_risk: boolean
  template_key: string
  /** Unsaved cached category; create/preview only, committed with the policy. */
  new_list?: { name: string; kind: 'geosite'; geosite_category: string; geosite_attrs: string }
}
export interface DestinationPolicyView extends DestinationPolicyInput {
  id: number
  priority: number
  created_at: number
  updated_at: number
  hits_recent: number | null
  last_hit_at: number | null
}
export interface DestinationPolicyOverviewItem extends DestinationPolicyView {
  list_states: Array<{ id: number; name: string; state: 'ready' | 'refreshing' | 'failed' | 'pending' | 'empty' | 'missing'; available?: boolean }>
  scope_missing: boolean
}
export interface DestinationPoliciesView {
  /** Facts from the selected published snapshot, independent of unsent edits. */
  published_generation: number
  published_has_access_control: boolean
  allow: DestinationPolicyOverviewItem[]
  block: DestinationPolicyOverviewItem[]
  observe: DestinationPolicyOverviewItem[]
  exemptions: { count: number }
  allowlist_groups: Array<{ group_id: number; name: string; stage: 'trial' | 'enforce'; stage_days: number }>
  hit_window_days: number
  budget: DestinationBudget
}
export async function getDestinationPolicies(opts: ReadOptions = {}): Promise<DestinationPoliciesView> {
  const { data } = await client.get<DestinationPoliciesView>('/admin/dest/policies', { signal: opts.signal, _skipErrorToast: opts.silent })
  if (!data || typeof data !== 'object' || !Array.isArray(data.allow) || !Array.isArray(data.block) ||
      !Array.isArray(data.observe) || !Array.isArray(data.allowlist_groups) || typeof data.published_has_access_control !== 'boolean') {
    throw new Error('invalid_destination_policies_response')
  }
  return data
}
export async function previewDestinationPolicy(input: DestinationPolicyInput & { id?: number; updated_at?: number }, signal?: AbortSignal): Promise<{ budget: DestinationBudget; new_list_preview?: DestinationListPreview }> {
  const { data } = await client.post<{ budget: DestinationBudget; new_list_preview?: DestinationListPreview }>('/admin/dest/policies/preview', input, { signal, _skipErrorToast: true })
  return data
}
export async function createDestinationPolicy(input: DestinationPolicyInput): Promise<DestinationPolicyView> {
  const { data } = await client.post<DestinationPolicyView>('/admin/dest/policies', input, { _skipErrorToast: true })
  return data
}
export async function putDestinationPolicy(id: number, input: DestinationPolicyInput & { updated_at: number }): Promise<DestinationPolicyView> {
  const { data } = await client.put<DestinationPolicyView>(`/admin/dest/policies/${id}`, input, { _skipErrorToast: true })
  return data
}
export async function deleteDestinationPolicy(id: number): Promise<void> {
  await client.delete(`/admin/dest/policies/${id}`, { _skipErrorToast: true })
}
export async function orderDestinationPolicies(action: DestinationPolicyAction, ids: number[]): Promise<void> {
  await client.put('/admin/dest/policies/order', { action, ids }, { _skipErrorToast: true })
}

export interface DestinationExemptionInput { reason: string; expires_at?: number | null }
export interface DestinationExemptionView {
  user_id: number
  upn: string | null
  reason: string
  created_by: number
  created_by_upn: string | null
  created_at: number
  expires_at: number | null
  expired: boolean
}
export async function getDestinationExemptions(opts: ReadOptions = {}): Promise<{ items: DestinationExemptionView[] }> {
  const { data } = await client.get<{ items: DestinationExemptionView[] }>('/admin/dest/exemptions', { signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export async function getDestinationExemption(userId: number, opts: ReadOptions = {}): Promise<DestinationExemptionView> {
  const { data } = await client.get<DestinationExemptionView>(`/admin/dest/exemptions/${userId}`, { signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export async function createDestinationExemption(input: DestinationExemptionInput & { user_id: number }): Promise<DestinationExemptionView> {
  const { data } = await client.post<DestinationExemptionView>('/admin/dest/exemptions', input, { _skipErrorToast: true })
  return data
}
export async function putDestinationExemption(userId: number, input: DestinationExemptionInput & { user_id?: number }): Promise<DestinationExemptionView> {
  const { data } = await client.put<DestinationExemptionView>(`/admin/dest/exemptions/${userId}`, input, { _skipErrorToast: true })
  return data
}
export async function deleteDestinationExemption(userId: number): Promise<void> {
  await client.delete(`/admin/dest/exemptions/${userId}`, { _skipErrorToast: true })
}
export interface DestinationGlobalExceptionInput { target: string; match: 'site' | 'host'; scope: 'global' }
export interface DestinationPublicationView {
  generation: number
  published_generation: number
  paused: boolean
  publish_error: { kind: string; used?: number; limit?: number; field?: string } | null
}
export async function publishDestinationPolicies(): Promise<DestinationPublicationView> {
  const { data } = await client.post<DestinationPublicationView>('/admin/dest/publish', undefined, { _skipErrorToast: true })
  return data
}
export async function putDestinationPause(paused: boolean): Promise<DestinationPublicationView> {
  const { data } = await client.put<DestinationPublicationView>('/admin/dest/pause', { paused }, { _skipErrorToast: true })
  return data
}
export interface DestinationUserAccessView {
  group: { id: number; name: string; mode: 'open' | 'allowlist'; stage: '' | 'trial' | 'enforce' } | null
  exemption: DestinationExemptionView | null
  hits_available: null
  recent_hits: null
  usage_available: null
  usage_nodes: null
}
export async function getDestinationUserAccess(userId: number, opts: ReadOptions = {}): Promise<DestinationUserAccessView> {
  const { data } = await client.get<DestinationUserAccessView>(`/admin/dest/users/${userId}`, { signal: opts.signal, _skipErrorToast: opts.silent })
  return data
}
export interface DestinationExceptionResult {
  list_id: number
  policy_id: number
  entry: string
  created?: { list_id: number; policy_id: number }
}
export async function createDestinationGlobalException(input: DestinationGlobalExceptionInput): Promise<DestinationExceptionResult> {
  const { data } = await client.post<DestinationExceptionResult>('/admin/dest/exceptions', input, { _skipErrorToast: true })
  return data
}

export interface DestinationAuditLosses {
  rows: number
  events: number
  unmatched: number
  scope: 'panel'
  complete: false
}

export interface DestinationNodeStatus {
  panel_id: number
  agent_id: string | null
  panel_name: string
  kind: string
  engine: string | null
  agent_version: string | null
  supports: { policy: boolean; hits: boolean; usage: boolean }
  collect: 'off' | 'hits' | 'hits_and_usage'
  collect_effective: '' | 'hits' | 'hits_and_usage'
  collecting: boolean
  state: DestinationNodeState
  fallback_reason: string
  fallback_exhausted: boolean
  minted_kind: '' | 'desired' | 'fallback' | 'empty' | 'paused'
  losses: DestinationAuditLosses | null
  over_limit: DestinationPublicationView['publish_error']
  sniffing_insufficient: Array<{ listener: string; label: string; node_id: number | null }>
  minted_at: number | null
  pending_since: number | null
  applied_at: number | null
  applied_rules: number
  allowlist_groups: Array<{ id: number; name: string | null }>
  last_report_at: number | null
  hits_24h: number | null
}
export interface DestinationStatus extends DestinationPublicationView {
  last_write_at: number | null
  next_publish_at: number | null
  apply_eta_ms: number
  totals: Record<DestinationNodeState | 'collecting' | 'total', number>
  nodes: DestinationNodeStatus[]
}
export async function getDestinationStatus(opts: ReadOptions = {}): Promise<DestinationStatus> {
  const { data } = await client.get<DestinationStatus>('/admin/dest/status', { signal: opts.signal, _skipErrorToast: opts.silent })
  if (!data || typeof data !== 'object' || !Array.isArray(data.nodes) ||
      !Number.isFinite(data.generation) || !Number.isFinite(data.published_generation)) {
    throw new Error('invalid_destination_status_response')
  }
  return data
}
