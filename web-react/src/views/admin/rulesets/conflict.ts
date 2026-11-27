import type { RuleSet } from '@/api/rules'

/** The fields other than `content` that a rule set carries and an editor can change (the slug is its identity). */
export const CONFLICT_FIELDS = [
  'name', 'sort', 'enabled', 'direct_subscription_domain', 'proxy_group_order', 'proxy_group_members', 'proxy_group_options', 'mihomo_sub_rules', 'mihomo_rematch_outbounds',
] as const
export type ConflictField = (typeof CONFLICT_FIELDS)[number] | 'content'

// An absent list and an empty one are the same thing to the server, so neither side "changed" it by being read back one way or the other.
const blank = (v: unknown) => v == null || (Array.isArray(v) && v.length === 0) || (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0)

/** Deep equality that ignores object key order (members and options are maps; the server may order their keys differently). */
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((x, i) => deepEqual(x, b[i]))
  const ka = Object.keys(a).filter(k => (a as Record<string, unknown>)[k] !== undefined)
  const kb = Object.keys(b).filter(k => (b as Record<string, unknown>)[k] !== undefined)
  return ka.length === kb.length && ka.every(k => deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]))
}
const same = (a: RuleSet, b: RuleSet, f: ConflictField) => (f === 'content' ? a.content === b.content : (blank(a[f]) && blank(b[f])) || deepEqual(a[f], b[f]))

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
