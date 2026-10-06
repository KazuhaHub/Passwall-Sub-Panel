import type { DestinationCategoriesView, DestinationPolicyInput } from '@/api/accessControl'
import { emptyPolicy } from './policyDraft'

export const policyTemplates = [
  { key: 'bt', action: 'block', risk: true, inline: { protocols: ['bittorrent'] } },
  { key: 'mail', action: 'block', risk: true, inline: { ports: '25,465,587', network: 'tcp' } },
  { key: 'private', action: 'block', risk: false, inline: { private: true } },
  { key: 'crypto', action: 'observe', risk: false, category: 'category-cryptocurrency' },
  { key: 'porn', action: 'observe', risk: false, category: 'category-porn' },
] as const
export type PolicyTemplate = typeof policyTemplates[number]
export function templatePolicy(template: PolicyTemplate, name: string, listName: string): DestinationPolicyInput {
  const inline = 'inline' in template ? { ...template.inline, ...('protocols' in template.inline ? { protocols: [...template.inline.protocols] } : {}) } : {}
  return { ...emptyPolicy(), name, action: template.action, enabled: true, counts_as_risk: template.risk, template_key: template.key,
    inline, ...('category' in template ? { new_list: { name: listName, kind: 'geosite', geosite_category: template.category, geosite_attrs: '' } } : {}) }
}
export function templateCategory(template: PolicyTemplate, catalog?: DestinationCategoriesView) {
  return 'category' in template ? catalog?.categories?.find(item => item.name === template.category) : undefined
}
