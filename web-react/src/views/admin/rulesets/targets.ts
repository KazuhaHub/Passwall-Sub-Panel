import type { Block } from '@/utils/ruleset/blocks'
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
export function targetChoices(blocks: readonly Block[], order: readonly string[], rematch: readonly string[]): TargetChoices {
  const rematchSet = new Set(rematch)
  const used = new Set<string>()
  for (const b of blocks) if (b.kind === 'rules' && b.target && !BUILTIN.has(b.target) && !rematchSet.has(b.target)) used.add(b.target)
  const groups = [...order.filter(n => used.has(n)), ...[...used].filter(n => !order.includes(n))]
  return { groups, rematch: [...rematch].filter(n => n !== '') }
}

/** Why a new group name cannot be used, as a locale key, or null. Same rules as the backend's validateMihomoSymbol plus "not taken". */
export function newGroupNameProblem(name: string, choices: TargetChoices): 'name_invalid' | 'name_taken' | null {
  if (name.trim() === '' || /[,\r\n]/.test(name)) return 'name_invalid'
  const n = name.trim()
  if (choices.groups.includes(n) || choices.rematch.includes(n) || BUILTIN.has(n)) return 'name_taken'
  return null
}

export function isBuiltinTarget(target: string): boolean {
  return BUILTIN.has(target)
}
