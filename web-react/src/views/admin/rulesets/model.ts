import type { RuleSet } from '@/api/rules'

export type ProxyGroupMembers = NonNullable<RuleSet['proxy_group_members']>
export type ProxyGroupOptionsMap = NonNullable<RuleSet['proxy_group_options']>

/** What "New rule set" starts from. The same values the old editor used: they are part of what a new rule set is saved with. */
export const EMPTY: RuleSet = {
  slug: '', name: '', sort: 100, enabled: true, direct_subscription_domain: false, proxy_group_order: [], proxy_group_members: {}, proxy_group_options: {}, mihomo_sub_rules: [], mihomo_rematch_outbounds: [], content: '',
}

export function cloneProxyGroupMembers(members: RuleSet['proxy_group_members']): ProxyGroupMembers {
  return Object.fromEntries(Object.entries(members || {}).map(([name, list]) => [name, list.map(member => ({ ...member }))]))
}

export function cloneProxyGroupOptions(options: RuleSet['proxy_group_options']): ProxyGroupOptionsMap {
  return Object.fromEntries(Object.entries(options || {}).map(([name, value]) => [name, { ...value }]))
}

export function cloneRuleSet(ruleSet: RuleSet): RuleSet {
  return {
    ...ruleSet,
    proxy_group_order: [...(ruleSet.proxy_group_order || [])],
    proxy_group_members: cloneProxyGroupMembers(ruleSet.proxy_group_members),
    proxy_group_options: cloneProxyGroupOptions(ruleSet.proxy_group_options),
    mihomo_sub_rules: (ruleSet.mihomo_sub_rules || []).map(rule => ({ ...rule })),
    mihomo_rematch_outbounds: (ruleSet.mihomo_rematch_outbounds || []).map(outbound => ({ ...outbound })),
  }
}

/** The draft a duplicate starts from: a copy with a new slug and name, saved as a NEW rule set. */
export function duplicateOf(rs: RuleSet): RuleSet {
  return { ...cloneRuleSet(rs), slug: rs.slug + '-copy', name: rs.name + ' (Copy)' }
}

/** Non-empty lines: what the table's "Lines" column counts. */
export function countLines(s: string): number {
  return s ? s.split('\n').filter(l => l.trim()).length : 0
}

/**
 * Whether the draft differs from the one the page opened with (what Save and the leave guard key on). The content, by far
 * the largest field (a rule set can be half a megabyte), is compared as the strings they are and the rest as before, so a
 * keystroke does not serialise the whole document.
 */
export function sameRuleSet(a: RuleSet, b: RuleSet): boolean {
  if (a.content !== b.content) return false
  return JSON.stringify({ ...a, content: '' }) === JSON.stringify({ ...b, content: '' })
}
