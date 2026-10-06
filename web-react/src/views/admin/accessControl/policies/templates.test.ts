import { expect, it } from 'vitest'
import { policyTemplates, templateCategory, templatePolicy } from './templates'

it('uses safe template defaults and creates no financial template', () => {
  const drafts = policyTemplates.map(template => templatePolicy(template, template.key, `${template.key} category`))
  expect(drafts.map(draft => [draft.template_key, draft.action, draft.counts_as_risk])).toEqual([
    ['bt', 'block', true], ['mail', 'block', true], ['private', 'block', false], ['crypto', 'observe', false], ['porn', 'observe', false],
  ])
  expect(drafts.every(draft => draft.enabled && draft.scope === 'all' && !draft.list_ids.length && !draft.group_ids.length)).toBe(true)
  expect(drafts[0].inline).toEqual({ protocols: ['bittorrent'] })
  expect(drafts[1].inline).toEqual({ ports: '25,465,587', network: 'tcp' })
  expect(drafts[2].inline).toEqual({ private: true })
  expect(drafts.slice(3).map(draft => draft.new_list?.geosite_category)).toEqual(['category-cryptocurrency', 'category-porn'])
})
it('keeps editable drafts independent and derives category counts only from the supplied catalog', () => {
  const bt = policyTemplates[0], crypto = policyTemplates[3]
  const draft = templatePolicy(bt, 'BT', 'unused')
  draft.inline.protocols!.push('edited')
  expect(templatePolicy(bt, 'BT again', 'unused').inline.protocols).toEqual(['bittorrent'])
  expect(templateCategory(crypto)).toBeUndefined()
  const category = { name: 'category-cryptocurrency', count: 17, regexp_count: 3, source_count: 19, ignored_broad_count: 2, attrs: ['!cn'] }
  expect(templateCategory(crypto, { categories: [category], updated_at: 10 })).toBe(category)
})
