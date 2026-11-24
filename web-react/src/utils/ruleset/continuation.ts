import type { ProxyGroupMember } from '@/api/rules'

/**
 * Whether a rule that names a target is the last word, or Mihomo may carry on matching below it. Mihomo follows an outbound
 * into the current choice of a group, and when that is PASS it goes on to the next rule (`match` in tunnel/tunnel.go); an
 * outbound that is a Rematch one sends it back through the rules; so a rule is not final just because it matched. The seeded
 * `🎮 UDP控制` group offers PASS on purpose.
 *
 * A subscriber can pick any member of a group, so a group may continue if any member can lead to PASS or to a Rematch
 * outbound, directly or through member groups. The members come from the backend's own inspection of the draft (the
 * effective members, defaults included), never from a guess made here; what the data does not cover is taken as may-continue.
 */
export interface Continuation {
  mayContinue(target: string): boolean
}

const FINAL = new Set(['DIRECT', 'REJECT', 'REJECT-DROP'])

export function continuation(groups: ReadonlyMap<string, readonly ProxyGroupMember[]> | null, rematch: readonly string[]): Continuation {
  const rem = new Set(rematch)
  const memo = new Map<string, boolean>()
  const walk = (name: string, seen: Set<string>): boolean => {
    if (FINAL.has(name)) return false
    if (name === 'PASS' || rem.has(name)) return true
    if (groups === null) return true
    const members = groups.get(name)
    if (!members) return true
    if (seen.has(name)) return false
    seen.add(name)
    return members.some(m => {
      if (m.kind === 'builtin') return m.value === 'PASS'
      if (m.kind === 'rematch') return true
      if (m.kind === 'proxy_group') return walk(m.value ?? '', seen)
      return false
    })
  }
  return {
    mayContinue(target) {
      const hit = memo.get(target)
      if (hit !== undefined) return hit
      const r = walk(target, new Set())
      memo.set(target, r)
      return r
    },
  }
}
