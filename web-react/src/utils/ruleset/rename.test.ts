import { describe, expect, it } from 'vitest'
import type { RuleSet } from '@/api/rules'
import { defaultRulesContent } from './defaultRules.fixture'
import { groupUsage, planRename } from './rename'

const base = (over: Partial<RuleSet> = {}): RuleSet => ({
  slug: 's', name: 'S', sort: 0, enabled: true, direct_subscription_domain: false,
  proxy_group_order: ['A', 'B'],
  proxy_group_members: { A: [{ kind: 'builtin', value: 'DIRECT' }], B: [{ kind: 'proxy_group', value: 'A' }, { kind: 'proxy_group', value: 'C' }] },
  proxy_group_options: { A: { type: 'url-test' } },
  mihomo_sub_rules: [{ name: 'sub1', content: '- DOMAIN,x.example,A\n- MATCH,B\n' }],
  content: '- DOMAIN,a.example,A\n- DOMAIN,b.example,A\n# n\n- DOMAIN,c.example,B\n- DOMAIN,d.example,A\n',
  ...over,
})

describe('groupUsage', () => {
  it('counts runs and rule lines of a group in the main rules and the sub-rules', () => {
    expect(groupUsage(base(), 'A')).toEqual({ blocks: 3, rules: 4 })
    expect(groupUsage(base(), 'B')).toEqual({ blocks: 2, rules: 2 })
    expect(groupUsage(base(), 'nobody')).toEqual({ blocks: 0, rules: 0 })
  })
})

describe('planRename', () => {
  it('rewrites every rule that names the group, in the main rules and the sub-rules, and nothing else', () => {
    const plan = planRename(base(), 'A', 'A2')
    expect(plan.ok).toBe(true)
    if (!plan.ok) return
    expect(plan.rules).toBe(4)
    expect(plan.patch.content).toBe('- DOMAIN,a.example,A2\n- DOMAIN,b.example,A2\n# n\n- DOMAIN,c.example,B\n- DOMAIN,d.example,A2\n')
    expect(plan.patch.mihomo_sub_rules).toEqual([{ name: 'sub1', content: '- DOMAIN,x.example,A2\n- MATCH,B\n' }])
  })

  it('moves the group\'s order entry, member list and options, and repoints members that reference it', () => {
    const plan = planRename(base(), 'A', 'A2')
    if (!plan.ok) throw new Error('expected ok')
    expect(plan.patch.proxy_group_order).toEqual(['A2', 'B'])
    expect(Object.keys(plan.patch.proxy_group_members!)).toEqual(['A2', 'B'])
    expect(plan.patch.proxy_group_members!.B).toEqual([{ kind: 'proxy_group', value: 'A2' }, { kind: 'proxy_group', value: 'C' }])
    expect(plan.patch.proxy_group_options).toEqual({ A2: { type: 'url-test' } })
    expect(plan.refs).toBe(1)
  })

  it('does not touch a field that has nothing to rename', () => {
    const plan = planRename(base({ proxy_group_options: undefined, mihomo_sub_rules: undefined, proxy_group_members: undefined }), 'A', 'A2')
    if (!plan.ok) throw new Error('expected ok')
    expect(plan.patch.proxy_group_options).toBeUndefined()
    expect(plan.patch.mihomo_sub_rules).toBeUndefined()
    expect(plan.patch.proxy_group_members).toBeUndefined()
  })

  it('refuses when the main rules are text-only (a foreign line): nothing may be written next to it', () => {
    const plan = planRename(base({ content: '- DOMAIN,a,A\n  ,DIRECT\n' }), 'A', 'A2')
    expect(plan).toEqual({ ok: false, reason: 'text_only' })
  })

  it('refuses when a line the editor cannot rewrite still names the group (it would quietly become a new group)', () => {
    expect(planRename(base({ content: "- DOMAIN,a,A\n- 'DOMAIN,b,A'\n" }), 'A', 'A2')).toEqual({ ok: false, reason: 'unreadable' })
    expect(planRename(base({ mihomo_sub_rules: [{ name: 'sub1', content: "- 'DOMAIN,b,A'\n" }] }), 'A', 'A2')).toEqual({ ok: false, reason: 'unreadable' })
    expect(planRename(base({ mihomo_sub_rules: [{ name: 'sub1', content: '- DOMAIN,b,A\n  ,DIRECT\n' }] }), 'A', 'A2')).toEqual({ ok: false, reason: 'unreadable' })
  })

  it('leaves a sub-rule with an unreadable line alone when it does not mention the group', () => {
    const plan = planRename(base({ mihomo_sub_rules: [{ name: 'sub1', content: "- 'host,b,Z'\n" }] }), 'A', 'A2')
    expect(plan.ok).toBe(true)
  })

  it('refuses to rename a group to itself', () => {
    expect(planRename(base(), 'A', 'A')).toEqual({ ok: false, reason: 'same' })
  })

  it('writes the seeded rule set\'s biggest group in one pass, byte-exact elsewhere', () => {
    const content = defaultRulesContent()
    const plan = planRename(base({ content, mihomo_sub_rules: [], proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {} }), '🚀 节点选择', '🚀 Proxy')
    if (!plan.ok) throw new Error('expected ok')
    expect(plan.rules).toBe(6904)
    const before = content.split('\n')
    const after = plan.patch.content!.split('\n')
    expect(after).toHaveLength(before.length)
    let changed = 0
    before.forEach((l, i) => { if (l !== after[i]) { changed++; expect(l.includes(',🚀 节点选择')).toBe(true) } })
    expect(changed).toBe(6904)
  })
})
