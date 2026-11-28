import type { Server } from '@/api/servers'

/**
 * The public addresses of a Passwall Node server, split by family. `reported` are the ones the node listed for its own
 * interfaces (any number of each family: a server can hold several public IPs); `observed` is the one address the panel
 * sees the node connect from, which may or may not be among them (a node behind NAT or a proxy is seen from an address
 * none of its interfaces carries). `reported` is false for a server that sent no list (3X-UI, S-UI, an older node), and
 * the caller then keeps showing what it always showed.
 */
export interface ServerAddresses {
  v4: string[]
  v6: string[]
  observed: string
  reported: boolean
}

export const isIPv6 = (address: string) => address.includes(':')

export function serverAddresses(s: Pick<Server, 'address' | 'interface_addresses'>): ServerAddresses {
  const seen = new Set<string>()
  const observed = s.address ?? ''
  const all = (s.interface_addresses ?? []).map(a => a.trim()).filter(a => a !== '' && !seen.has(a) && seen.add(a))
  // The address the panel sees the node connect from leads its family when the node also reports it: on a machine with
  // several public IPs that is the one actually in use, so it is the one the compact line should show.
  const lead = (xs: string[]) => (xs.includes(observed) ? [observed, ...xs.filter(x => x !== observed)] : xs)
  return {
    v4: lead(all.filter(a => !isIPv6(a))),
    v6: lead(all.filter(isIPv6)),
    observed,
    reported: all.length > 0,
  }
}

/** What the compact line shows: the first of each family, and how many more there are behind the "+N". */
export function addressHeadline(a: ServerAddresses): { shown: string[]; more: number } {
  const shown = [a.v4[0], a.v6[0]].filter((x): x is string => !!x)
  return { shown, more: a.v4.length + a.v6.length - shown.length }
}
