import { describe, expect, it } from 'vitest'
import { defaultRulesContent } from './defaultRules.fixture'
import { evalRule, parseIp, prepareProbe, routeFor, safeRegex, type ProbeInput } from './match'
import { parseDoc, parseLine } from './parse'

const input = (over: Partial<ProbeInput> = {}): ProbeInput => ({ address: 'example.com', port: '443', network: 'tcp', resolvedIp: '', process: '', srcPort: '', srcIp: '', ...over })
const probe = (over: Partial<ProbeInput> = {}) => {
  const p = prepareProbe(input(over))
  if (!p.ok) throw new Error('probe: ' + p.problem)
  return p.probe
}
const ev = (rule: string, over: Partial<ProbeInput> = {}) => {
  const line = parseLine('- ' + rule)
  if (!line.rule) throw new Error('not a rule: ' + rule)
  return evalRule(line.rule, probe(over))
}
const yes = { verdict: 'yes' }
const no = { verdict: 'no' }
const unknown = (reason: string) => ({ verdict: 'unknown', reason })

describe('the probe', () => {
  it('lower-cases and trims the address, drops a trailing dot and the brackets round an IPv6 address', () => {
    expect(probe({ address: '  Example.COM. ' })).toMatchObject({ kind: 'domain', host: 'example.com' })
    expect(probe({ address: '[2001:db8::1]' })).toMatchObject({ kind: 'ip' })
    expect(probe({ address: '1.2.3.4' })).toMatchObject({ kind: 'ip' })
  })
  it('refuses what is not an address and a port out of range, with the reason', () => {
    expect(prepareProbe(input({ address: '' }))).toEqual({ ok: false, problem: 'address_empty' })
    expect(prepareProbe(input({ address: 'a b' }))).toEqual({ ok: false, problem: 'address_invalid' })
    expect(prepareProbe(input({ address: 'https://x.com/a' }))).toEqual({ ok: false, problem: 'address_invalid' })
    expect(prepareProbe(input({ port: '70000' }))).toEqual({ ok: false, problem: 'port_invalid' })
    expect(prepareProbe(input({ resolvedIp: 'nope' }))).toEqual({ ok: false, problem: 'resolved_invalid' })
  })
  it('reads IPv4, IPv6 and mapped addresses', () => {
    expect(parseIp('1.2.3.4')).toEqual({ v: 4, n: 0x01020304n })
    expect(parseIp('::1')).toEqual({ v: 6, n: 1n })
    expect(parseIp('2001:db8::')?.v).toBe(6)
    expect(parseIp('::ffff:1.2.3.4')).toEqual({ v: 6, n: 0xffff01020304n })
    expect(parseIp('1.2.3')).toBeNull()
    expect(parseIp('1.2.3.256')).toBeNull()
    expect(parseIp('1::2::3')).toBeNull()
  })
})

describe('domain rules', () => {
  it.each([
    ['DOMAIN,example.com,X', 'example.com', yes],
    ['DOMAIN,example.com,X', 'www.example.com', no],
    ['DOMAIN-SUFFIX,example.com,X', 'example.com', yes],
    ['DOMAIN-SUFFIX,example.com,X', 'a.b.example.com', yes],
    ['DOMAIN-SUFFIX,example.com,X', 'notexample.com', no],
    ['DOMAIN-KEYWORD,xamp,X', 'example.com', yes],
    ['DOMAIN-KEYWORD,xamp,X', 'other.org', no],
    ['domain-suffix,EXAMPLE.com,X', 'Example.COM', yes],
  ])('%s against %s', (rule, address, expected) => expect(ev(rule, { address })).toEqual(expected))

  it('a connection to an IP address has no domain: the domain rules do not match it', () => {
    expect(ev('DOMAIN-SUFFIX,4,X', { address: '1.2.3.4' })).toEqual(no)
    expect(ev('DOMAIN-KEYWORD,1.2,X', { address: '1.2.3.4' })).toEqual(no)
  })

  it('DOMAIN-REGEX is tested as a search in the host name, and refused when it could not be tested safely', () => {
    expect(ev('DOMAIN-REGEX,^ad[0-9]+\\.example\\.com$,X', { address: 'ad12.example.com' })).toEqual(yes)
    expect(ev('DOMAIN-REGEX,^ad[0-9]+\\.example\\.com$,X', { address: 'www.example.com' })).toEqual(no)
    expect(ev('DOMAIN-REGEX,(a+)+$,X', { address: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaab.com' })).toEqual(unknown('regex'))
    expect(ev('DOMAIN-REGEX,(?=a)b,X')).toEqual(unknown('regex'))
    expect(ev('DOMAIN-REGEX,(a)\\1,X')).toEqual(unknown('regex'))
    expect(ev('DOMAIN-REGEX,[,X')).toEqual(unknown('regex'))
  })
})

describe('safeRegex', () => {
  it('accepts plain patterns and refuses nested quantifiers, alternation under a quantifier, look-around, back-references and long patterns', () => {
    expect(safeRegex('^.*\\.example\\.com$')).toBeInstanceOf(RegExp)
    expect(safeRegex('^(www\\.)?example\\.com$')).toBeInstanceOf(RegExp)
    for (const bad of ['(a+)+', '(a*)*', '(a|aa)+', '([a-z]+\\.)+com', '(?=x)', '(?!x)', '(?<=x)', '(a)\\1', '(?<n>a)', 'x'.repeat(300)]) expect(safeRegex(bad), bad).toBeNull()
  })
})

describe('address rules', () => {
  it('match an IP address directly, v4 and v6, and never across the families', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve', { address: '10.1.2.3' })).toEqual(yes)
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve', { address: '11.1.2.3' })).toEqual(no)
    expect(ev('IP-CIDR,0.0.0.0/0,X', { address: '2001:db8::1' })).toEqual(no)
    expect(ev('IP-CIDR6,2001:db8::/32,X', { address: '2001:db8::1' })).toEqual(yes)
    expect(ev('IP-CIDR6,fc00::/7,X', { address: 'fd12::1' })).toEqual(yes)
    expect(ev('IP-CIDR6,::/0,X', { address: '1.2.3.4' })).toEqual(no)
    expect(ev('IP-CIDR,1.2.3.4/32,X', { address: '1.2.3.4' })).toEqual(yes)
  })

  it('a domain against a rule with no-resolve is not checked: no resolution is made for it', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve')).toEqual(no)
  })

  it('a domain against a rule without no-resolve: Mihomo resolves it there, so the answer needs the resolved address', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X')).toEqual(unknown('resolve'))
    expect(ev('IP-CIDR,10.0.0.0/8,X', { resolvedIp: '10.1.1.1' })).toEqual(yes)
    expect(ev('IP-CIDR,10.0.0.0/8,X', { resolvedIp: '11.1.1.1' })).toEqual(no)
  })

  it('a no-resolve rule that the resolved address would fit depends on whether an earlier rule already resolved it: not decided', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve', { resolvedIp: '10.1.1.1' })).toEqual(unknown('resolve'))
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve', { resolvedIp: '11.1.1.1' })).toEqual(no)
  })

  it('a rule marked src is about the source address, which is only known when it was given', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,src')).toEqual(unknown('input'))
    expect(ev('IP-CIDR,10.0.0.0/8,X,src', { srcIp: '10.2.2.2' })).toEqual(yes)
    expect(ev('SRC-IP-CIDR,10.0.0.0/8,X', { srcIp: '11.2.2.2' })).toEqual(no)
    expect(ev('SRC-IP-CIDR,10.0.0.0/8,X')).toEqual(unknown('input'))
  })

  it('a prefix-less or malformed range is not guessed at', () => {
    expect(ev('IP-CIDR,10.0.0.1,X', { address: '10.0.0.1' })).toEqual(unknown('unsupported'))
    expect(ev('IP-CIDR,10.0.0.0/33,X', { address: '10.0.0.1' })).toEqual(unknown('unsupported'))
  })
})

describe('port, network, process', () => {
  it('DST-PORT takes a port or a range', () => {
    expect(ev('DST-PORT,443,X')).toEqual(yes)
    expect(ev('DST-PORT,80,X')).toEqual(no)
    expect(ev('DST-PORT,8000-9000,X', { port: '8080' })).toEqual(yes)
    expect(ev('DST-PORT,8000-9000,X', { port: '9001' })).toEqual(no)
    expect(ev('DST-PORT,443,X', { port: '' })).toEqual(unknown('input'))
    expect(ev('DST-PORT,a,X')).toEqual(unknown('unsupported'))
  })
  it('SRC-PORT needs the source port', () => {
    expect(ev('SRC-PORT,5000,X', { srcPort: '5000' })).toEqual(yes)
    expect(ev('SRC-PORT,5000,X', { srcPort: '5001' })).toEqual(no)
    expect(ev('SRC-PORT,5000,X')).toEqual(unknown('input'))
  })
  it('NETWORK is tcp or udp', () => {
    expect(ev('NETWORK,udp,X', { network: 'udp' })).toEqual(yes)
    expect(ev('NETWORK,UDP,X', { network: 'udp' })).toEqual(yes)
    expect(ev('NETWORK,udp,X', { network: 'tcp' })).toEqual(no)
  })
  it('PROCESS-NAME needs a process name; a name that differs only in case is not decided', () => {
    expect(ev('PROCESS-NAME,curl,X', { process: 'curl' })).toEqual(yes)
    expect(ev('PROCESS-NAME,curl,X', { process: 'wget' })).toEqual(no)
    expect(ev('PROCESS-NAME,curl,X', { process: 'CURL' })).toEqual(unknown('input'))
    expect(ev('PROCESS-NAME,curl,X')).toEqual(unknown('input'))
  })
  it('MATCH always matches', () => expect(ev('MATCH,Final')).toEqual(yes))
})

describe('what a browser cannot decide', () => {
  it.each([
    ['GEOIP,CN,X', 'client_data'], ['GEOSITE,cn,X', 'client_data'], ['RULE-SET,name,X', 'client_data'], ['IP-ASN,13335,X', 'client_data'], ['SRC-GEOIP,CN,X', 'client_data'],
    ['SUB-RULE,(NETWORK,UDP),sub1', 'sub_rule'], ['REMATCH-NAME,x,X', 'sub_rule'],
    ['DOMAIN-WILDCARD,*.example.com,X', 'unsupported'], ['PROCESS-PATH,/bin/curl,X', 'unsupported'], ['TYPO-SUFFIX,a,X', 'unsupported'],
  ])('%s is reported as not decidable (%s)', (rule, reason) => expect(ev(rule)).toEqual(unknown(reason)))
})

describe('logical rules', () => {
  it('AND, OR and NOT over what can be decided', () => {
    expect(ev('AND,((NETWORK,UDP),(DST-PORT,443)),X', { network: 'udp' })).toEqual(yes)
    expect(ev('AND,((NETWORK,UDP),(DST-PORT,443)),X', { network: 'tcp' })).toEqual(no)
    expect(ev('OR,((NETWORK,UDP),(DST-PORT,80)),X', { network: 'tcp', port: '80' })).toEqual(yes)
    expect(ev('OR,((NETWORK,UDP),(DST-PORT,80)),X', { network: 'tcp', port: '81' })).toEqual(no)
    expect(ev('NOT,((DOMAIN-SUFFIX,example.com)),X')).toEqual(no)
    expect(ev('NOT,((DOMAIN-SUFFIX,other.com)),X')).toEqual(yes)
  })
  it('a nested logical rule', () => {
    expect(ev('AND,((DOMAIN-SUFFIX,example.com),(OR,((DST-PORT,80),(DST-PORT,443)))),X')).toEqual(yes)
    expect(ev('AND,((DOMAIN-SUFFIX,example.com),(OR,((DST-PORT,80),(DST-PORT,8080)))),X')).toEqual(no)
  })
  it('an unknown part decides nothing unless the rest already does', () => {
    expect(ev('AND,((GEOIP,CN),(NETWORK,udp)),X', { network: 'tcp' })).toEqual(no)
    expect(ev('AND,((GEOIP,CN),(NETWORK,udp)),X', { network: 'udp' })).toEqual(unknown('client_data'))
    expect(ev('OR,((GEOIP,CN),(NETWORK,udp)),X', { network: 'udp' })).toEqual(yes)
    expect(ev('OR,((GEOIP,CN),(NETWORK,udp)),X', { network: 'tcp' })).toEqual(unknown('client_data'))
    expect(ev('NOT,((GEOIP,CN)),X')).toEqual(unknown('client_data'))
  })
  it('an expression the reader cannot take apart is not guessed at', () => {
    expect(ev('AND,(NETWORK,UDP,X')).toEqual(unknown('unsupported'))
    expect(ev('AND,garbage,X')).toEqual(unknown('unsupported'))
    expect(ev('NOT,((NETWORK,UDP),(DST-PORT,443)),X')).toEqual(unknown('unsupported'))
  })
})

describe('the route through a rule list', () => {
  const D = (s: string) => parseDoc(s)

  it('finds the first rule that matches and the later ones that would have', () => {
    const doc = D('- DOMAIN-SUFFIX,a.com,P\n- DOMAIN-SUFFIX,example.com,Q\n- DOMAIN,www.example.com,R\n- DOMAIN-KEYWORD,example,S\n- MATCH,F\n')
    const r = routeFor(doc, probe({ address: 'www.example.com' }))
    expect(r.first).toEqual({ line: 1, target: 'Q' })
    expect(r.certain).toBe(true)
    expect(r.later.map(l => l.line)).toEqual([2, 3, 4])
  })

  it('no rule at all matches', () => {
    const r = routeFor(D('- DOMAIN,a.com,P\n'), probe())
    expect(r.first).toBeNull()
    expect(r.certain).toBe(true)
  })

  it('an undecidable line before the match makes everything after it uncertain, and says which lines', () => {
    const doc = D('- GEOSITE,cn,CN\n- DOMAIN-SUFFIX,example.com,Q\n- MATCH,F\n')
    const r = routeFor(doc, probe())
    expect(r.first).toEqual({ line: 1, target: 'Q' })
    expect(r.certain).toBe(false)
    expect(r.maybe).toEqual([{ line: 0, target: 'CN', reason: 'client_data' }])
  })

  it('an undecidable line after the match changes nothing', () => {
    const r = routeFor(D('- DOMAIN-SUFFIX,example.com,Q\n- GEOSITE,cn,CN\n'), probe())
    expect(r.certain).toBe(true)
    expect(r.maybe).toEqual([])
  })

  it('skips comments, blank lines and lines it cannot read, and counts lines from zero as the document does', () => {
    const r = routeFor(D('# note\n\n- DOMAIN,a.com,P\n- \'DOMAIN,example.com,Z\'\n- DOMAIN,example.com,Q\n'), probe())
    expect(r.first).toEqual({ line: 4, target: 'Q' })
  })

  it('the seeded rule set: copilot.microsoft.com goes to Bing, and the Ai rule at line 1947 never applies, nor does the fallback', () => {
    const doc = parseDoc(defaultRulesContent())
    const r = routeFor(doc, probe({ address: 'copilot.microsoft.com' }))
    expect(r.first).toEqual({ line: 1784, target: 'Ⓜ️ 微软Bing' })
    expect(r.later.map(l => [l.line + 1, l.target])).toEqual([[1803, 'Ⓜ️ 微软服务'], [1947, '💬 Ai平台'], [2235, '🌍 国外媒体'], [6242, '🚀 节点选择'], [9491, '🐟 漏网之鱼']])
    // Two lines are not decidable in a browser (GEOSITE, GEOIP) and both come after, so the answer stands.
    expect(r.certain).toBe(true)
  })

  it('the seeded rule set: 1.1.1.1 over UDP 443 meets the QUIC rule, 1.1.1.1 over TCP 443 falls to something later', () => {
    const doc = parseDoc(defaultRulesContent())
    const quic = routeFor(doc, probe({ address: '1.1.1.1', network: 'udp' }))
    expect(quic.first).toEqual({ line: 23, target: '⚡ QUIC控制' })
    expect(quic.certain).toBe(true)
    const tcp = routeFor(doc, probe({ address: '1.1.1.1', network: 'tcp' }))
    expect(tcp.first!.line).toBeGreaterThan(23)
  })

  it('evaluating all 9,474 rules for a probe is quick', () => {
    const doc = parseDoc(defaultRulesContent())
    const t0 = performance.now()
    for (let i = 0; i < 20; i++) routeFor(doc, probe({ address: `host${i}.nowhere.example` }))
    expect((performance.now() - t0) / 20).toBeLessThan(60)
  })
})
