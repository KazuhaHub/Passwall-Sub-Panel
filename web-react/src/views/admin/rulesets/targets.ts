import type { Block } from '@/utils/ruleset/blocks'
import { canWriteField } from '@/utils/ruleset/scalar'
import { BUILTIN_TARGETS } from '@/utils/ruleset/types'

export interface TargetChoices {
  /** Proxy groups the rules already use, in the order the "Proxy groups" tab shows them. */
  groups: string[]
  rematch: string[]
}

const BUILTIN = new Set<string>(BUILTIN_TARGETS)

/**
 * The outbounds a rule can point at, from what the rule set itself says. Proxy groups have no list of their own: a
 * name that appears as a target and is not a built-in outbound or a Rematch outbound IS a group (the backend finds
 * them the same way), so a typo here would quietly make a new one, which is why the picker lists the existing ones
 * and makes a new group a deliberate step.
 */
/*
 * What is NOT offered, and why (checked against internal/service/render, 2026-11):
 * - Subscription lines. The backend does resolve a line name used as a rule target (line_names.go lineNameCatalog.resolve), but
 *   by the line's name AS RENDERED, which carries the `{{ factor }}` placeholder and the region flag the template switches on
 *   (newLineNameCatalog: base is the render item's name). The inspection data has only the stored display name, so the
 *   editor could not write a name that is certain to resolve, and an unresolvable one makes the subscription of a user it does not resolve for fail to
 *   render (ambiguous / unavailable line errors, line_names.go) instead of failing at save. A line is made a member of a proxy group on the
 *   Proxy groups tab, which stores a node and line identity; a rule then points at that group. A line name a rule already has stays what the picker shows.
 * - Sub-rules. A sub-rule name is the third field of a `SUB-RULE` line, which has no outbound at all (targetIndex returns -1 for
 *   it); named as the target of an ordinary rule it would simply become a proxy group with that name.
 */
export function targetChoices(blocks: readonly Block[], order: readonly string[], rematch: readonly string[]): TargetChoices {
  const rematchSet = new Set(rematch)
  const used = new Set<string>()
  for (const b of blocks) if (b.kind === 'rules' && b.target && !BUILTIN.has(b.target) && !rematchSet.has(b.target)) used.add(b.target)
  const groups = [...order.filter(n => used.has(n)), ...[...used].filter(n => !order.includes(n))]
  return { groups, rematch: [...rematch].filter(n => n !== '') }
}

/** Why a new group name cannot be used, as a locale key, or null. Same rules as the backend's validateMihomoSymbol plus "not taken". */
export function newGroupNameProblem(name: string, choices: TargetChoices): 'name_invalid' | 'name_taken' | null {
  const n = name.trim()
  // Whatever the model would not write into a line is not a name either (a ` #`, a quote...).
  if (canWriteField(n) || /^(no-resolve|src)$/i.test(n)) return 'name_invalid'
  if (choices.groups.includes(n) || choices.rematch.includes(n) || BUILTIN.has(n)) return 'name_taken'
  return null
}

export function isBuiltinTarget(target: string): boolean {
  return BUILTIN.has(target)
}
