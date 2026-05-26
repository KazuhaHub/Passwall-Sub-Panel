import { client } from './client'
import type { ListResponse } from './types'

export interface RuleSet {
  slug: string
  name: string
  sort: number
  enabled: boolean
  proxy_group_order: string[]
  content: string
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
