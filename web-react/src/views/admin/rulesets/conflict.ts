import type { RuleSet } from '@/api/rules'

/** The fields other than `content` that a rule set carries and an editor can change (the slug is its identity). */
export const CONFLICT_FIELDS = [
  'name', 'sort', 'enabled', 'direct_subscription_domain', 'proxy_group_order', 'proxy_group_members', 'proxy_group_options', 'mihomo_sub_rules', 'mihomo_rematch_outbounds',
] as const
export type ConflictField = (typeof CONFLICT_FIELDS)[number] | 'content'

// An absent list and an empty one are the same thing to the server, so neither side "changed" it by being read back one way or the other.
const norm = (v: unknown) => {
  const j = JSON.stringify(v ?? null)
  return j === '[]' || j === '{}' ? 'null' : j
}
const same = (a: RuleSet, b: RuleSet, f: ConflictField) => (f === 'content' ? a.content === b.content : norm(a[f]) === norm(b[f]))

/** What differs between two versions: `content` first (compared as the strings they are), then the other fields. */
export function changedFields(from: RuleSet, to: RuleSet): ConflictField[] {
  return (['content', ...CONFLICT_FIELDS] as ConflictField[]).filter(f => !same(from, to, f))
}

/**
 * The fields both sides changed from the shared base and left different from each other: the ones a merge could not settle
 * by itself. A field both changed to the same value is not a disagreement.
 */
export function changedByBoth(base: RuleSet, theirs: RuleSet, mine: RuleSet): ConflictField[] {
  const t = new Set(changedFields(base, theirs))
  return changedFields(base, mine).filter(f => t.has(f) && !same(theirs, mine, f))
}
