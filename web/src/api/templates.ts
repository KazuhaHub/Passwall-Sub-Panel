import { client } from './client'

export interface Template {
  slug: string
  name: string
  client_type: string
  is_default: boolean
  rule_sets: string[]
  content: string
}

export async function listTemplates() {
  const { data } = await client.get<{ items: Template[] }>('/admin/templates')
  return data.items
}

export async function getTemplate(slug: string) {
  const { data } = await client.get<Template>(`/admin/templates/${slug}`)
  return data
}

export async function saveTemplate(t: Template) {
  await client.put(`/admin/templates/${t.slug}`, t)
}

export async function deleteTemplate(slug: string) {
  await client.delete(`/admin/templates/${slug}`)
}
