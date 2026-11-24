import { describe, expect, it } from 'vitest'
import type { ProxyGroupMember, RuleSet } from '@/api/rules'
import { defaultRulesContent } from './defaultRules.fixture'
import { groupUsage, planRename, type RenamePlan } from './rename'

const b = (value: string): ProxyGroupMember => ({ kind: 'builtin', value })
const g = (value: string): ProxyGroupMember => ({ kind: 'proxy_group', value })
const all: ProxyGroupMember = { kind: 'node_set', value: 'remaining' }

const baseSet = (over: Partial<RuleSet> = {}): RuleSet => ({
  slug: 's', name: 'S', sort: 0, enabled: true, direct_subscription_domain: false,
  proxy_group_order: ['A', 'B'],
  proxy_group_members: { A: [b('DIRECT')], B: [g('A'), g('C')] },
  proxy_group_options: { A: { type: 'url-test' } },
  mihomo_sub_rules: [{ name: 'sub1', content: '- DOMAIN,x.example,A\n- MATCH,B\n' }],
  content: '- DOMAIN,a.example,A\n- DOMAIN,b.example,A\n# n\n- DOMAIN,c.example,B\n- DOMAIN,d.example,A\n',
  ...over,
})
/** What the backend's inspection says: explicit members where there are some, else the name-derived defaults the test supplies. */
const effectiveOf = (rs: RuleSet, defaults: Record<string, ProxyGroupMember[]> = {}) => {
  const out = new Map<string, ProxyGroupMember[]>()
  for (const name of Object.keys(defaults)) out.set(name, rs.proxy_group_members?.[name] ?? defaults[name])
  for (const [name, list] of Object.entries(rs.proxy_group_members ?? {})) out.set(name, list)
  return out
}
const ok = (p: RenamePlan) => { if (!p.ok) throw new Error('expected ok, got ' + p.reason); return p }
const apply = (rs: RuleSet, p: Extract<RenamePlan, { ok: true }>): RuleSet => ({ ...rs, ...p.patch })

describe('groupUsage', () => {
  it('counts runs and rule lines of a group in the main rules and the sub-rules', () => {
    expect(groupUsage(baseSet(), 'A')).toEqual({ blocks: 3, rules: 4 })
    expect(groupUsage(baseSet(), 'B')).toEqual({ blocks: 2, rules: 2 })
    expect(groupUsage(baseSet(), 'nobody')).toEqual({ blocks: 0, rules: 0 })
  })

  it('counts a quoted target as the group it names (review B)', () => {
    const rs = baseSet({ content: "- DOMAIN,a,Proxy\n- DOMAIN,b,'Proxy'\n- DOMAIN,c,\"Proxy\"\n", mihomo_sub_rules: [] })
    expect(groupUsage(rs, 'Proxy')).toEqual({ blocks: 1, rules: 3 })
  })
})

describe('planRename', () => {
  const rs = baseSet()
  const eff = effectiveOf(rs, { A: [], B: [] })

  it('rewrites every rule that names the group, in the main rules and the sub-rules, and nothing else', () => {
    const plan = ok(planRename(rs, 'A', 'A2', eff))
    expect(plan.rules).toBe(4)
    expect(plan.patch.content).toBe('- DOMAIN,a.example,A2\n- DOMAIN,b.example,A2\n# n\n- DOMAIN,c.example,B\n- DOMAIN,d.example,A2\n')
    expect(plan.patch.mihomo_sub_rules).toEqual([{ name: 'sub1', content: '- DOMAIN,x.example,A2\n- MATCH,B\n' }])
  })

  it('moves the group\'s order entry, member list and options, and repoints members that reference it', () => {
    const plan = ok(planRename(rs, 'A', 'A2', eff))
    expect(plan.patch.proxy_group_order).toEqual(['A2', 'B'])
    expect(Object.keys(plan.patch.proxy_group_members!)).toEqual(['A2', 'B'])
    expect(plan.patch.proxy_group_members!.B).toEqual([g('A2'), g('C')])
    expect(plan.patch.proxy_group_options).toEqual({ A2: { type: 'url-test' } })
    expect(plan.refs).toBe(1)
    expect(plan.materialised).toBe(0)
  })

  it('does not touch a field that has nothing to rename', () => {
    const lean = baseSet({ proxy_group_options: undefined, mihomo_sub_rules: undefined, proxy_group_members: undefined })
    const plan = ok(planRename(lean, 'A', 'A2', new Map([['A', [b('DIRECT')]]])))
    expect(plan.patch.proxy_group_options).toBeUndefined()
    expect(plan.patch.mihomo_sub_rules).toBeUndefined()
  })

  it('refuses when the main rules are text-only (a foreign line): nothing may be written next to it', () => {
    expect(planRename(baseSet({ content: '- DOMAIN,a,A\n  ,DIRECT\n' }), 'A', 'A2', eff)).toEqual({ ok: false, reason: 'text_only' })
  })

  it('refuses when a line the editor cannot rewrite still names the group (it would quietly become a new group)', () => {
    expect(planRename(baseSet({ content: "- DOMAIN,a,A\n- 'DOMAIN,b,A'\n" }), 'A', 'A2', eff)).toEqual({ ok: false, reason: 'unreadable' })
    expect(planRename(baseSet({ mihomo_sub_rules: [{ name: 'sub1', content: "- 'DOMAIN,b,A'\n" }] }), 'A', 'A2', eff)).toEqual({ ok: false, reason: 'unreadable' })
    expect(planRename(baseSet({ mihomo_sub_rules: [{ name: 'sub1', content: '- DOMAIN,b,A\n  ,DIRECT\n' }] }), 'A', 'A2', eff)).toEqual({ ok: false, reason: 'unreadable' })
  })

  it('leaves a sub-rule with an unreadable line alone when it does not name the group', () => {
    expect(planRename(baseSet({ mihomo_sub_rules: [{ name: 'sub1', content: "- 'host,b,Z'\n" }] }), 'A', 'A2', eff).ok).toBe(true)
  })

  it('a text line that merely contains the name inside a longer one does not stop the rename (whole-name match)', () => {
    const rs2 = baseSet({ content: "- DOMAIN,a,A\n- 'DOMAIN,b,ProxyAll'\n", mihomo_sub_rules: [] })
    expect(planRename(rs2, 'A', 'A2', effectiveOf(rs2, { A: [b('DIRECT')] })).ok).toBe(true)
    const rs3 = baseSet({ content: "- DOMAIN,a,Proxy\n- 'DOMAIN,b,ProxyAll'\n", mihomo_sub_rules: [] })
    expect(planRename(rs3, 'Proxy', 'P2', effectiveOf(rs3, { Proxy: [b('DIRECT')] })).ok).toBe(true)
    const rs4 = baseSet({ content: "- DOMAIN,a,Proxy\n- 'DOMAIN,b,Proxy'\n", mihomo_sub_rules: [] })
    expect(planRename(rs4, 'Proxy', 'P2', effectiveOf(rs4, { Proxy: [b('DIRECT')] }))).toEqual({ ok: false, reason: 'unreadable' })
  })

  it('refuses to rename a group to itself', () => {
    expect(planRename(rs, 'A', 'A', eff)).toEqual({ ok: false, reason: 'same' })
  })

  it('refuses a name that stale member or option settings already carry, whatever the order of the keys', () => {
    const stale = baseSet({ proxy_group_members: { A: [b('DIRECT')], Old: [b('REJECT')] } })
    expect(planRename(stale, 'A', 'Old', effectiveOf(stale, { A: [] }))).toEqual({ ok: false, reason: 'taken' })
    const staleOpt = baseSet({ proxy_group_options: { Old: { type: 'url-test' } } })
    expect(planRename(staleOpt, 'A', 'Old', effectiveOf(staleOpt, { A: [], B: [] }))).toEqual({ ok: false, reason: 'taken' })
    const first = baseSet({ proxy_group_members: { Old: [b('REJECT')], A: [b('DIRECT')] } })
    expect(planRename(first, 'A', 'Old', effectiveOf(first, { A: [] }))).toEqual({ ok: false, reason: 'taken' })
  })

  it('refuses without the backend\'s effective members for the group, or for a group nothing but members names', () => {
    expect(planRename(rs, 'A', 'A2', new Map())).toEqual({ ok: false, reason: 'no_data' })
    expect(planRename(rs, 'Ghost', 'G2', eff)).toEqual({ ok: false, reason: 'unused' })
  })

  it('writes the seeded rule set\'s biggest group in one pass, byte-exact elsewhere', () => {
    const content = defaultRulesContent()
    const seeded = baseSet({ content, mihomo_sub_rules: [], proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {} })
    const plan = ok(planRename(seeded, '🚀 节点选择', '🚀 Proxy', new Map([['🚀 节点选择', [b('DIRECT'), all]]])))
    expect(plan.rules).toBe(6904)
    const before = content.split('\n')
    const after = plan.patch.content!.split('\n')
    expect(after).toHaveLength(before.length)
    let changed = 0
    before.forEach((l, i) => { if (l !== after[i]) { changed++; expect(l.includes(',🚀 节点选择')).toBe(true) } })
    expect(changed).toBe(6904)
  })
})

describe('a rename leaves every group\'s effective members as they were, the name apart (review A)', () => {
  // A seeded-like set: no member entries at all, the members come from the group NAMES (proxyGroupChoices).
  const rs = baseSet({
    content: '- DOMAIN,ad.example,🛑 广告拦截\n- DOMAIN,u.example,🎮 UDP控制\n- DOMAIN,ai.example,💬 Ai平台\n- DOMAIN,n.example,🚀 节点选择\n- MATCH,🐟 漏网之鱼\n',
    proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {}, mihomo_sub_rules: [],
  })
  const defaults: Record<string, ProxyGroupMember[]> = {
    '🛑 广告拦截': [b('REJECT'), b('DIRECT')],
    '🎮 UDP控制': [b('DIRECT'), b('PASS'), g('🚀 节点选择'), b('REJECT')],
    '💬 Ai平台': [g('🚀 节点选择'), all, b('DIRECT')],
    '🚀 节点选择': [b('DIRECT'), all],
    '🐟 漏网之鱼': [g('🚀 节点选择'), b('DIRECT'), all],
  }
  const eff = effectiveOf(rs, defaults)
  const swap = (list: readonly ProxyGroupMember[], from: string, to: string) => list.map(m => (m.kind === 'proxy_group' && m.value === from ? { ...m, value: to } : m))

  it('repro 1: renaming 🛑 广告拦截 to Ads keeps REJECT first (it is written out under the new name, not left to the name heuristics)', () => {
    const plan = ok(planRename(rs, '🛑 广告拦截', 'Ads', eff))
    expect(plan.patch.proxy_group_members!.Ads).toEqual([b('REJECT'), b('DIRECT')])
    expect(plan.materialised).toBe(1)
  })

  it('repro 2: renaming 🚀 节点选择 rewrites every default member list that named it, so the backend does not make it again', () => {
    const plan = ok(planRename(rs, '🚀 节点选择', 'Nodes', eff))
    const m = plan.patch.proxy_group_members!
    expect(m['🎮 UDP控制']).toEqual([b('DIRECT'), b('PASS'), g('Nodes'), b('REJECT')])
    expect(m['💬 Ai平台']).toEqual([g('Nodes'), all, b('DIRECT')])
    expect(m['🐟 漏网之鱼']).toEqual([g('Nodes'), b('DIRECT'), all])
    expect(m.Nodes).toEqual([b('DIRECT'), all])
    expect(m['🛑 广告拦截']).toBeUndefined()
    expect(plan.materialised).toBe(4)
  })

  it('repro 3: renaming 🎮 UDP控制 likewise', () => {
    const plan = ok(planRename(rs, '🎮 UDP控制', 'Udp', eff))
    expect(plan.patch.proxy_group_members!.Udp).toEqual([b('DIRECT'), b('PASS'), g('🚀 节点选择'), b('REJECT')])
  })

  it.each(['🛑 广告拦截', '🎮 UDP控制', '💬 Ai平台', '🚀 节点选择', '🐟 漏网之鱼'])('after renaming %s every group\'s members equal what they were, modulo the name', from => {
    const plan = ok(planRename(rs, from, 'Renamed', eff))
    const after = apply(rs, plan)
    for (const [name, members] of eff) {
      const nowName = name === from ? 'Renamed' : name
      const explicit = after.proxy_group_members?.[nowName]
      // Anything that would not follow its own old name-derived default (the group itself, or one that names the renamed group) is explicit.
      if (name === from || members.some(m => m.kind === 'proxy_group' && m.value === from)) {
        expect(explicit, `${name} must be explicit`).toEqual(swap(members, from, 'Renamed'))
      }
    }
    // Nothing is left that names the old group.
    const text = JSON.stringify(after.proxy_group_members ?? {})
    expect(text.includes(`"${from}"`)).toBe(false)
  })

  it('a list that is already explicit is repointed, not counted as made explicit', () => {
    const own = baseSet({ content: rs.content, proxy_group_order: [], proxy_group_members: { '🎮 UDP控制': [g('🚀 节点选择'), b('DIRECT')] }, proxy_group_options: {}, mihomo_sub_rules: [] })
    const plan = ok(planRename(own, '🚀 节点选择', 'Nodes', effectiveOf(own, defaults)))
    expect(plan.patch.proxy_group_members!['🎮 UDP控制']).toEqual([g('Nodes'), b('DIRECT')])
    expect(plan.refs).toBe(1)
    expect(plan.materialised).toBe(3)
  })
})

describe('quoted targets are the group they name (review B)', () => {
  it('the repro: Proxy, \'Proxy\' and "Proxy" are all rewritten, each in its own quotes', () => {
    const rs = baseSet({ content: "- DOMAIN,a.com,Proxy\n- DOMAIN,b.com,'Proxy'\n- DOMAIN,c.com,\"Proxy\"\n", mihomo_sub_rules: [], proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {} })
    const plan = ok(planRename(rs, 'Proxy', 'P2', new Map([['Proxy', [b('DIRECT')]]])))
    expect(plan.rules).toBe(3)
    expect(plan.patch.content).toBe("- DOMAIN,a.com,P2\n- DOMAIN,b.com,'P2'\n- DOMAIN,c.com,\"P2\"\n")
  })
})
