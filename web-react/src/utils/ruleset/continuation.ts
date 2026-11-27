import type { ProxyGroupMember } from '@/api/rules'

/**
 * Whether a rule that names a target is the last word, or Mihomo may carry on matching below it. Mihomo follows an outbound
 * into the current choice of a group, and when that is PASS it goes on to the next rule (`match` in tunnel/tunnel.go); an
 * outbound that is a Rematch one sends it back through the rules; so a rule is not final just because it matched. The seeded
 * `🎮 UDP控制` group offers PASS on purpose.
 *
 * Over UDP there is one more way on: when the chosen proxy cannot carry UDP, Mihomo skips the rule and tries the next (tunnel.go),
 * so for a UDP connection any group with a node, a line or a node set among its members may continue too. Built-in outbounds
 * (DIRECT, REJECT) are never skipped that way. This is not modelled further: which proxy is chosen is the subscriber's.
 *
 * A subscriber can pick any member of a group, so a group may continue if any member can lead to PASS or to a Rematch
 * outbound, directly or through member groups. The members come from the backend's own inspection of the draft (the
 * effective members, defaults included), never from a guess made here; what the data does not cover is taken as may-continue.
 */
export interface Continuation {
  mayContinue(target: string, udp?: boolean): boolean
}

const FINAL = new Set(['DIRECT', 'REJECT', 'REJECT-DROP'])

export function continuation(groups: ReadonlyMap<string, readonly ProxyGroupMember[]> | null, rematch: readonly string[]): Continuation {
  const rem = new Set(rematch)
  const memo = new Map<string, boolean>()
  const walk = (name: string, seen: Set<string>, udp: boolean): boolean => {
    if (FINAL.has(name)) return false
    if (name === 'PASS' || rem.has(name)) return true
    if (groups === null) return true
    const members = groups.get(name)
    if (!members) return true
    if (seen.has(name)) return false
    seen.add(name)
    return members.some(m => {
      if (udp && (m.kind === 'node' || m.kind === 'line' || m.kind === 'node_set')) return true
      if (m.kind === 'builtin') return m.value === 'PASS'
      if (m.kind === 'rematch') return true
      if (m.kind === 'proxy_group') return walk(m.value ?? '', seen, udp)
      return false
    })
  }
  return {
    mayContinue(target, udp = false) {
      const key = (udp ? 'u:' : 't:') + target
      const hit = memo.get(key)
      if (hit !== undefined) return hit
      const r = walk(target, new Set(), udp)
      memo.set(key, r)
      return r
    },
  }
}
