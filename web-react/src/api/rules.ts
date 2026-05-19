import { client } from './client'

export interface RuleSet {
  slug: string
  name: string
  sort: number
  enabled: boolean
  proxy_group_order: string[]
  content: string
}

export async function listRuleSets() {
  const { data } = await client.get<{ items: RuleSet[] }>('/admin/rules')
  return data.items
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
