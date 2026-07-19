import { client } from './client'
import type { ListResponse } from './types'

export interface RuleSet {
  slug: string
  name: string
  sort: number
  enabled: boolean
  proxy_group_order: string[]
  proxy_group_members?: Record<string, ProxyGroupMember[]>
  proxy_group_options?: Record<string, ProxyGroupOptions>
  content: string
}

export interface ProxyGroupMember {
  kind: 'builtin' | 'proxy_group' | 'node' | 'node_set'
  value?: string
  node_id?: number
}

export type ProxyGroupType = 'select' | 'url-test' | 'fallback' | 'load-balance'
export type LoadBalanceStrategy = 'round-robin' | 'consistent-hashing' | 'sticky-sessions'

export interface ProxyGroupOptions {
  type: ProxyGroupType
  url?: string
  interval?: number
  lazy?: boolean
  timeout?: number
  tolerance?: number
  strategy?: LoadBalanceStrategy
}

export interface ProxyGroupIssue {
  level: 'error' | 'warning'
  group?: string
  code?: string
  params?: Record<string, string | number>
  message: string
}

export interface ProxyGroupInspectNode {
  id: number
  display_name: string
  server_address: string
  region: string
  tags: string[]
  enabled: boolean
}

export interface ProxyGroupInspectGroup {
  name: string
  configured: boolean
  options_configured: boolean
  options: ProxyGroupOptions
  default_members: ProxyGroupMember[]
  members: ProxyGroupMember[]
  preview: string[]
}

export interface ProxyGroupInspection {
  groups: ProxyGroupInspectGroup[]
  builtins: string[]
  nodes: ProxyGroupInspectNode[]
  regions: string[]
  tags: string[]
  issues: ProxyGroupIssue[]
}

export interface RuleSetListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listRuleSets(params: RuleSetListParams = {}, signal?: AbortSignal) {
  const merged = { page: 1, page_size: 200, ...params }
  const { data } = await client.get<ListResponse<RuleSet>>('/admin/rules', { params: merged, signal })
  return data
}

export async function getRuleSet(slug: string) {
  const { data } = await client.get<RuleSet>(`/admin/rules/${slug}`)
  return data
}

export async function saveRuleSet(rs: RuleSet) {
  await client.put(`/admin/rules/${rs.slug}`, rs)
}

export async function inspectProxyGroups(req: {
  content: string
  proxy_group_members: Record<string, ProxyGroupMember[]>
  proxy_group_options: Record<string, ProxyGroupOptions>
  preview_group_id?: number
}, signal?: AbortSignal) {
  const { data } = await client.post<ProxyGroupInspection>('/admin/rules/inspect-proxy-groups', req, { signal })
  return data
}

export async function deleteRuleSet(slug: string) {
  await client.delete(`/admin/rules/${slug}`)
}

// resetRuleSet overwrites the on-disk yaml with the binary's embedded
// seed copy. 404 means the slug is admin-created and has no canonical
// fallback — UI hides the affordance in that case.
export async function resetRuleSet(slug: string) {
  await client.post(`/admin/rules/${slug}/reset`)
}

// SEEDED_RULESET_SLUGS mirrors internal/seed/files/rulesets/. Keep in
// sync with the Go side.
export const SEEDED_RULESET_SLUGS = ['default_rules']
