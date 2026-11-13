// THE ONE-LINE GRAMMAR OF WHAT A POLICY MATCHES (plan §7.4 S3). Lists, IP
// ranges, private addresses and BitTorrent are alternatives ("or"); the ports
// and the network narrow them ("and"):
//   (list "X" or IP ranges 2 or BT) and port 25 and TCP
// Pure: the words come in through `t`, so a test holds the grammar and the
// bundle holds the language.

type T = (key: string, opts?: Record<string, unknown>) => string

export interface MatchInput {
  listNames: string[]
  cidrs: number
  private: boolean
  bittorrent: boolean
  ports: string
  network: string
}

export interface MatchSummary {
  alternatives: { kind: 'list' | 'cidrs' | 'private' | 'bt'; value: string | number }[]
  narrowing: { kind: 'ports' | 'network'; value: string }[]
  /** BitTorrent beside a list, IP ranges or private addresses is two wire rules (x1 / x2); ports and network are in both. */
  split: boolean
}

export function matchSummary(i: MatchInput): MatchSummary {
  const alternatives: MatchSummary['alternatives'] = []
  for (const name of i.listNames) alternatives.push({ kind: 'list', value: name })
  if (i.cidrs > 0) alternatives.push({ kind: 'cidrs', value: i.cidrs })
  if (i.private) alternatives.push({ kind: 'private', value: '' })
  const addressSide = alternatives.length > 0
  if (i.bittorrent) alternatives.push({ kind: 'bt', value: '' })
  const narrowing: MatchSummary['narrowing'] = []
  if (i.ports.trim()) narrowing.push({ kind: 'ports', value: i.ports.trim() })
  if (i.network === 'tcp' || i.network === 'udp') narrowing.push({ kind: 'network', value: i.network.toUpperCase() })
  return { alternatives, narrowing, split: i.bittorrent && addressSide }
}

const P = 'admin:access_control.policies.summary.'

export function summaryText(t: T, s: MatchSummary): string {
  const alts = s.alternatives.map(a => t(`${P}${a.kind}`, { value: a.value }))
  const nars = s.narrowing.map(n => t(`${P}${n.kind}`, { value: n.value }))
  if (alts.length === 0 && nars.length === 0) return ''
  const left = alts.length > 1 && nars.length > 0 ? `(${alts.join(t(`${P}or`))})` : alts.join(t(`${P}or`))
  return [left, ...nars].filter(Boolean).join(t(`${P}and`))
}
