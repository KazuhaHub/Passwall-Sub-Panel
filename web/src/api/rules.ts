import { client } from './client'

export interface RuleSet {
  slug: string
  name: string
  sort: number
  enabled: boolean
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
