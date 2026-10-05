import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { UISettings } from './settings'

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
  samples: Array<{ line: number; text: string; reason: string }>
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
  list_states: Array<{ id: number; name: string; state: 'ready' | 'refreshing' | 'failed' | 'pending' | 'empty' | 'missing' }>
  scope_missing: boolean
}
export interface DestinationPoliciesView {
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
  return data
}
export async function previewDestinationPolicy(input: DestinationPolicyInput & { id?: number; updated_at?: number }, signal?: AbortSignal): Promise<{ budget: DestinationBudget }> {
  const { data } = await client.post<{ budget: DestinationBudget }>('/admin/dest/policies/preview', input, { signal, _skipErrorToast: true })
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
