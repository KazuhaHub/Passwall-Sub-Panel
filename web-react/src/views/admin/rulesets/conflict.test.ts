import { describe, expect, it } from 'vitest'
import type { RuleSet } from '@/api/rules'
import { changedFields } from './conflict'

const rs = (extra: Partial<RuleSet>): RuleSet => ({ slug: 'a', name: 'A', sort: 1, enabled: true, direct_subscription_domain: false, proxy_group_order: [], content: '', ...extra })

describe('changedFields', () => {
  it('does not call a map changed because its keys came back in another order', () => {
    const a = rs({ proxy_group_members: { X: [{ kind: 'builtin', value: 'DIRECT' }], Y: [] }, proxy_group_options: { X: { type: 'select', url: 'u' } } })
    const b = rs({ proxy_group_members: { Y: [], X: [{ value: 'DIRECT', kind: 'builtin' }] }, proxy_group_options: { X: { url: 'u', type: 'select' } } })
    expect(changedFields(a, b)).toEqual([])
  })
  it('sees a real difference, and treats absent and empty as the same', () => {
    expect(changedFields(rs({ proxy_group_members: { X: [] } }), rs({ proxy_group_members: { X: [{ kind: 'builtin', value: 'DIRECT' }] } }))).toEqual(['proxy_group_members'])
    expect(changedFields(rs({}), rs({ mihomo_sub_rules: [], proxy_group_options: {} }))).toEqual([])
  })
})
