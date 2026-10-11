import { client } from './client'
import type { DestinationAuditLosses, DestinationBudget } from './accessControl'
import type { ReadOptions } from './requestOptions'

export type DestinationGroupStage = '' | 'trial' | 'enforce'
export type DestinationGroupExclusionReason = 'xui' | 'sui' | 'capability' | 'over_limit' | 'rejected' | 'sniffing'
export interface DestinationGroupNode { panel_id: number; name: string }
export interface DestinationGroupCoverage {
  total_nodes: number
  eligible_nodes: number
  ineligible: Array<DestinationGroupNode & { reason: DestinationGroupExclusionReason }>
}
export interface DestinationGroupMode {
  group_id: number
  mode: 'open' | 'allowlist'
  stage: DestinationGroupStage
  list_ids: number[]
  base_list_id: number
  extra_list_id: number
  stage_changed_at: number | null
  updated_at: number
}
export interface DestinationGroupSummary extends DestinationGroupMode, DestinationGroupCoverage {
  group_name: string
  members: number
  applied_nodes: number
  would_deny_recent: { days: number; sites: number; count: number }
  denied_24h: number
}
export interface DestinationGroupTrialSite { site: string; count: number; last_at: number; added: boolean }
export interface DestinationGroupReadiness {
  lists_ready: boolean
  not_ready_lists: Array<{ id: number; name: string }>
  unapplied_nodes: DestinationGroupNode[]
  base_nonempty: boolean
  remaining_sites_24h: number
  top_sites: DestinationGroupTrialSite[]
  trial_losses: DestinationAuditLosses
}
export interface DestinationGroupDetail extends DestinationGroupSummary { readiness: DestinationGroupReadiness }
export interface DestinationGroupPreview extends DestinationGroupCoverage {
  members: number
  base_hosts: string[]
  base_hosts_available: boolean
  base_list_id: number
  extra_list_id: number
  budget: DestinationBudget
}
export interface DestinationGroupTrialReport {
  effective_days: number
  items: DestinationGroupTrialSite[]
  dropped_in_range: number
  losses: DestinationAuditLosses
}
export type DestinationGroupInput = ({ mode: 'open'; stage: '' } | { mode: 'allowlist'; stage: 'trial' | 'enforce' }) & {
  list_ids: number[]
  updated_at: number
  base_hosts?: string[]
}

const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const count = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0
const safeCount = (v: unknown): v is number => count(v) && Number.isSafeInteger(v)
const id = (v: unknown): v is number => safeCount(v) && v > 0
const instant = (v: unknown): v is number => id(v) && v <= 8.64e15
const version = (v: unknown): v is number => v === 0 || instant(v)
const ids = (v: unknown): v is number[] => Array.isArray(v) && v.every(id) && new Set(v).size === v.length
const node = (v: unknown): v is DestinationGroupNode => record(v) && id(v.panel_id) && typeof v.name === 'string'
const uniqueNodes = (v: unknown): v is DestinationGroupNode[] => Array.isArray(v) && v.every(node) && new Set(v.map(n => n.panel_id)).size === v.length
const losses = (v: unknown): v is DestinationAuditLosses => record(v) && v.scope === 'panel' && v.complete === false && count(v.rows) && count(v.events) && count(v.unmatched)
const modeValid = (mode: unknown, stage: unknown) => mode === 'open' && stage === '' || mode === 'allowlist' && (stage === 'trial' || stage === 'enforce')

function mode(v: unknown): v is DestinationGroupMode {
  return record(v) && id(v.group_id) && modeValid(v.mode, v.stage) && ids(v.list_ids) && safeCount(v.base_list_id) && safeCount(v.extra_list_id) &&
    (v.base_list_id === 0 || v.base_list_id !== v.extra_list_id) && (v.mode !== 'allowlist' || v.base_list_id > 0 && v.extra_list_id > 0) &&
    version(v.updated_at) && (v.stage_changed_at === null || instant(v.stage_changed_at))
}
function coverage(v: unknown): v is DestinationGroupCoverage {
  if (!record(v) || !safeCount(v.total_nodes) || !safeCount(v.eligible_nodes) || v.eligible_nodes > v.total_nodes || !uniqueNodes(v.ineligible) || v.ineligible.length !== v.total_nodes - v.eligible_nodes) return false
  return v.ineligible.every(n => record(n) && ['xui', 'sui', 'capability', 'over_limit', 'rejected', 'sniffing'].includes(String(n.reason)))
}
function summary(v: unknown): v is DestinationGroupSummary {
  if (!record(v) || !mode(v) || !coverage(v) || typeof v.group_name !== 'string' || !count(v.members) || !safeCount(v.applied_nodes) || v.applied_nodes > v.eligible_nodes || v.mode === 'open' && v.applied_nodes !== 0 || !count(v.denied_24h)) return false
  const recent = v.would_deny_recent
  return record(recent) && id(recent.days) && recent.days <= 30 && count(recent.sites) && count(recent.count)
}
function trialSite(v: unknown): v is DestinationGroupTrialSite {
  return record(v) && Object.keys(v).every(k => ['site', 'count', 'last_at', 'added'].includes(k)) &&
    typeof v.site === 'string' && v.site.length > 0 && v.site.length <= 253 && count(v.count) && v.count > 0 && instant(v.last_at) && typeof v.added === 'boolean'
}
function trialSites(v: unknown): v is DestinationGroupTrialSite[] {
  return Array.isArray(v) && v.every(trialSite) && new Set(v.map(item => item.site)).size === v.length
}
function detail(v: unknown): v is DestinationGroupDetail {
  if (!record(v) || !summary(v) || !record(v.readiness)) return false
  const r = v.readiness
  return typeof r.lists_ready === 'boolean' && Array.isArray(r.not_ready_lists) && r.not_ready_lists.every(l => record(l) && id(l.id) && typeof l.name === 'string') &&
    r.lists_ready === (r.not_ready_lists.length === 0) && uniqueNodes(r.unapplied_nodes) && r.unapplied_nodes.length === (v.mode === 'allowlist' ? v.eligible_nodes - v.applied_nodes : 0) &&
    typeof r.base_nonempty === 'boolean' && count(r.remaining_sites_24h) && trialSites(r.top_sites) && r.top_sites.length <= 3 && r.top_sites.length <= r.remaining_sites_24h && r.top_sites.every(s => !s.added) && losses(r.trial_losses)
}
function preview(v: unknown): v is DestinationGroupPreview {
  if (!record(v) || !coverage(v) || !count(v.members) || !Array.isArray(v.base_hosts) || !v.base_hosts.every(h => typeof h === 'string') || typeof v.base_hosts_available !== 'boolean' || !safeCount(v.base_list_id) || !safeCount(v.extra_list_id) || !record(v.budget)) return false
  const b = v.budget
  return (v.base_hosts_available || v.base_hosts.length === 0) && ['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'].every(k => record(b[k]) && count(b[k].used) && count(b[k].limit) && b[k].limit > 0)
}
function report(v: unknown): v is DestinationGroupTrialReport {
  return record(v) && id(v.effective_days) && v.effective_days <= 30 && trialSites(v.items) && count(v.dropped_in_range) && losses(v.losses) && v.dropped_in_range === v.losses.rows
}
function requireID(value: number) { if (!id(value)) throw new Error('Invalid destination group ID') }
const readConfig = (opts: ReadOptions) => ({ signal: opts.signal, _skipErrorToast: opts.silent })
const malformed = () => new Error('Invalid destination group response')

export async function getDestinationGroups(opts: ReadOptions = {}): Promise<{ items: DestinationGroupSummary[] }> {
  const { data } = await client.get<unknown>('/admin/dest/groups', readConfig(opts))
  if (!record(data) || !Array.isArray(data.items) || !data.items.every(summary) || new Set(data.items.map(g => g.group_id)).size !== data.items.length) throw malformed()
  return { items: data.items }
}
export async function getDestinationGroup(groupID: number, opts: ReadOptions = {}): Promise<DestinationGroupDetail> {
  requireID(groupID)
  const { data } = await client.get<unknown>(`/admin/dest/groups/${groupID}`, readConfig(opts))
  if (!detail(data) || data.group_id !== groupID) throw malformed()
  return data
}
export async function previewDestinationGroup(groupID: number, listIDs: number[], opts: ReadOptions = {}): Promise<DestinationGroupPreview> {
  requireID(groupID)
  if (!Array.isArray(listIDs) || !listIDs.every(id)) throw new Error('Invalid destination list IDs')
  const selected = [...new Set(listIDs)].sort((a, b) => a - b)
  const { data } = await client.get<unknown>(`/admin/dest/groups/${groupID}/preview`, { params: { list_ids: selected.join(',') }, ...readConfig(opts) })
  if (!preview(data)) throw malformed()
  return data
}
export async function getDestinationGroupTrialReport(groupID: number, days: number, opts: ReadOptions = {}): Promise<DestinationGroupTrialReport> {
  requireID(groupID)
  if (!id(days)) throw new Error('Invalid destination report days')
  const { data } = await client.get<unknown>(`/admin/dest/groups/${groupID}/trial-report`, { params: { days }, ...readConfig(opts) })
  if (!report(data)) throw malformed()
  return data
}
export async function putDestinationGroup(groupID: number, input: DestinationGroupInput): Promise<DestinationGroupMode> {
  requireID(groupID)
  if (!modeValid(input.mode, input.stage) || !ids(input.list_ids) || !version(input.updated_at) || input.base_hosts !== undefined && (!Array.isArray(input.base_hosts) || !input.base_hosts.every(h => typeof h === 'string'))) throw new Error('Invalid destination group input')
  const { data } = await client.put<unknown>(`/admin/dest/groups/${groupID}`, input)
  if (!mode(data) || data.group_id !== groupID) throw malformed()
  return data
}
export async function createDestinationGroupException(groupID: number, input: { target: string; match: 'site' | 'host' }): Promise<{ group_id: number; list_id: number; entry: string }> {
  requireID(groupID)
  const { data } = await client.post<unknown>('/admin/dest/exceptions', { ...input, scope: 'group', group_id: groupID })
  if (!record(data) || data.group_id !== groupID || !id(data.list_id) || typeof data.entry !== 'string' || data.entry.length === 0) throw malformed()
  return { group_id: groupID, list_id: data.list_id, entry: data.entry }
}
