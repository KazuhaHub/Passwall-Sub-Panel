import { client } from './client'
import type { ListResponse } from './types'

export interface Template {
  slug: string
  name: string
  client_type: string
  is_default: boolean
  rule_sets: string[]
  proxy_group_order?: string[]
  content: string
}

export interface TemplateListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listTemplates(params: TemplateListParams = {}, signal?: AbortSignal) {
  const merged = { page: 1, page_size: 200, ...params }
  const { data } = await client.get<ListResponse<Template>>('/admin/templates', { params: merged, signal })
  return data
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

// resetTemplate overwrites the on-disk yaml with the binary's embedded
// seed copy. 404 means the slug is admin-created (no canonical fallback)
// — the UI should hide the reset affordance in that case to avoid the
// pointless round-trip. The backend stores templates as one yaml file
// per slug under <ConfigDir>/templates/.
export async function resetTemplate(slug: string) {
  await client.post(`/admin/templates/${slug}/reset`)
}

// SEEDED_TEMPLATE_SLUGS mirrors internal/seed/files/templates/. Keep in
// sync with the Go side — the only known seeds today, no need to round-
// trip the binary to learn the list.
export const SEEDED_TEMPLATE_SLUGS = ['default-mihomo', 'default-sing-box']
