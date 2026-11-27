import { describe, expect, it } from 'vitest'
import { defaultRulesContent } from './defaultRules.fixture'
import { continuation, type Continuation } from './continuation'
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
/** Only PASS goes on; everything else is the last word (the tests below that are about groups say otherwise). */
const FINALS: Continuation = { mayContinue: t => t.toUpperCase() === 'PASS' }
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

  it('a domain against a rule with no-resolve is not resolved for it; with no address entered the rule is not decided (Mihomo tests any address the connection already holds)', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,no-resolve')).toEqual(unknown('input'))
  })

  it('options are compared exactly, as Mihomo does: NO-RESOLVE and SRC are not no-resolve and src', () => {
    expect(ev('IP-CIDR,10.0.0.0/8,X,NO-RESOLVE')).toEqual(unknown('resolve'))
    expect(ev('IP-CIDR,10.0.0.0/8,X,NO-RESOLVE', { resolvedIp: '10.1.1.1' })).toEqual(yes)
    expect(ev('IP-CIDR,10.0.0.0/8,X,SRC', { srcIp: '11.1.1.1', resolvedIp: '10.1.1.1' })).toEqual(yes)
  })

  it('a prefix with a leading zero is refused, as netip.ParsePrefix refuses it', () => {
    expect(ev('IP-CIDR,08.8.8.0/24,X', { address: '8.8.8.8' })).toEqual(unknown('unsupported'))
    expect(ev('IP-CIDR,8.8.8.0/024,X', { address: '8.8.8.8' })).toEqual(unknown('unsupported'))
    expect(parseIp('01.2.3.4')).toBeNull()
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
  it('PROCESS-NAME needs a process name and compares it without regard to case (strings.EqualFold)', () => {
    expect(ev('PROCESS-NAME,curl,X', { process: 'curl' })).toEqual(yes)
    expect(ev('PROCESS-NAME,curl,X', { process: 'wget' })).toEqual(no)
    expect(ev('PROCESS-NAME,curl,X', { process: 'CURL' })).toEqual(yes)
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
    const r = routeFor(doc, probe({ address: 'www.example.com' }), FINALS)
    expect(r.first).toEqual({ line: 1, target: 'Q' })
    expect(r.certain).toBe(true)
    expect(r.later.map(l => l.line)).toEqual([2, 3, 4])
  })

  it('no rule at all matches', () => {
    const r = routeFor(D('- DOMAIN,a.com,P\n'), probe(), FINALS)
    expect(r.first).toBeNull()
    expect(r.certain).toBe(true)
  })

  it('an undecidable line before the match makes everything after it uncertain, and says which lines', () => {
    const doc = D('- GEOSITE,cn,CN\n- DOMAIN-SUFFIX,example.com,Q\n- MATCH,F\n')
    const r = routeFor(doc, probe(), FINALS)
    expect(r.first).toEqual({ line: 1, target: 'Q' })
    expect(r.certain).toBe(false)
    expect(r.maybe).toEqual([{ line: 0, target: 'CN', reason: 'client_data' }])
  })

  it('an undecidable line after the match changes nothing', () => {
    const r = routeFor(D('- DOMAIN-SUFFIX,example.com,Q\n- GEOSITE,cn,CN\n'), probe(), FINALS)
    expect(r.certain).toBe(true)
    expect(r.maybe).toEqual([])
  })

  it('skips comments, blank lines and lines it cannot read, and counts lines from zero as the document does', () => {
    const r = routeFor(D('# note\n\n- DOMAIN,a.com,P\n- \'DOMAIN,example.com,Z\'\n- DOMAIN,example.com,Q\n'), probe(), FINALS)
    expect(r.first).toEqual({ line: 4, target: 'Q' })
  })

  it('the seeded rule set: copilot.microsoft.com goes to Bing, and the Ai rule at line 1947 never applies, nor does the fallback', () => {
    const doc = parseDoc(defaultRulesContent())
    const r = routeFor(doc, probe({ address: 'copilot.microsoft.com' }), FINALS)
    expect(r.first).toEqual({ line: 1784, target: 'Ⓜ️ 微软Bing' })
    expect(r.later.map(l => [l.line + 1, l.target])).toEqual([[1803, 'Ⓜ️ 微软服务'], [1947, '💬 Ai平台'], [2235, '🌍 国外媒体'], [6242, '🚀 节点选择'], [9491, '🐟 漏网之鱼']])
    // The private-range rules at the top say no-resolve, and with no address entered they are not decided (the connection may already
    // hold one); nothing else above the match is undecided.
    expect(r.certain).toBe(false)
    expect(r.maybe.length).toBeGreaterThan(0)
    expect(new Set(r.maybe.map(m => m.reason))).toEqual(new Set(['input']))
    // Two lines are not decidable in a browser (GEOSITE, GEOIP) and both come after; with an address entered the answer stands.
    const withIp = routeFor(doc, probe({ address: 'copilot.microsoft.com', resolvedIp: '203.0.113.9' }), FINALS)
    expect(withIp.first).toEqual({ line: 1784, target: 'Ⓜ️ 微软Bing' })
    expect(withIp.certain).toBe(true)
  })

  it('the seeded rule set: 1.1.1.1 over UDP 443 meets the QUIC rule, 1.1.1.1 over TCP 443 falls to something later', () => {
    const doc = parseDoc(defaultRulesContent())
    const quic = routeFor(doc, probe({ address: '1.1.1.1', network: 'udp' }), FINALS)
    expect(quic.first).toEqual({ line: 23, target: '⚡ QUIC控制' })
    expect(quic.certain).toBe(true)
    const tcp = routeFor(doc, probe({ address: '1.1.1.1', network: 'tcp' }), FINALS)
    expect(tcp.first!.line).toBeGreaterThan(23)
  })

  it('evaluating all 9,474 rules for a probe is quick', () => {
    const doc = parseDoc(defaultRulesContent())
    const t0 = performance.now()
    for (let i = 0; i < 20; i++) routeFor(doc, probe({ address: `host${i}.nowhere.example` }), FINALS)
    expect((performance.now() - t0) / 20).toBeLessThan(60)
  })
})

describe('DOMAIN-REGEX is what Mihomo runs: regexp2, ignoring case, a conservative subset only (review C)', () => {
  it('ignores case, as Mihomo compiles it', () => {
    expect(ev('DOMAIN-REGEX,^www\\.Google\\.com$,A', { address: 'www.google.com' })).toEqual(yes)
    expect(ev('DOMAIN-REGEX,^www\\.google\\.com$,A', { address: 'WWW.GOOGLE.COM' })).toEqual(yes)
  })

  it('the repro: a regex the tester decides gives the answer Mihomo gives', () => {
    const doc = parseDoc('- DOMAIN-REGEX,^www\\.Google\\.com$,A\n- MATCH,B\n')
    const r = routeFor(doc, probe({ address: 'www.google.com' }), FINALS)
    expect(r.first).toEqual({ line: 0, target: 'A' })
    expect(r.certain).toBe(true)
  })

  it.each([
    ['\\Agoogle\\.com', 'an anchor JS reads as the letter A'], ['google\\.com\\Z', ''], ['google\\z', ''], ['\\Ggoogle', ''],
    ['(?<n>a)b', 'named group'], ['(?=a)b', 'look-ahead'], ['(?<=a)b', 'look-behind'], ['(?!a)b', ''],
    ['(a)\\1', 'back-reference'], ['(?i)abc', 'inline flag'], ['(?s)a.b', ''], ['a*?b', 'lazy'], ['a+?b', ''], ['a*+b', 'possessive'], ['(?>a)b', 'atomic'],
    ['\\p{L}+', 'Unicode class'], ['[^]a', 'empty negated class'], ['[a-z-[aeiou]]', 'set subtraction'], ['[[:alpha:]]', 'POSIX class'],
    ['\\bgoogle', 'word boundary'], ['a\\x41', 'hex escape'], ['a\\u0041', ''], ['a\\n', ''], ['\\k<n>', ''],
    ['(', 'unbalanced'], ['a)', ''], ['*a', 'nothing to repeat'], ['a**', ''], ['a{,3}', ''], ['a{2', ''], ['[a', ''],
  ])('refuses %j (%s)', (pattern) => {
    expect(safeRegex(pattern)).toBeNull()
    expect(ev(`DOMAIN-REGEX,${pattern.replace(/,/g, '\\x2c')},X`)).toEqual(unknown('regex'))
  })

  it.each([
    '^www\\.google\\.com$', 'google', '^(www\\.)?google\\.(com|cn)$', '^[a-z0-9-]+\\.example\\.com$', '^ad\\d+\\.example\\.com$', '^.*\\.example\\.com$', '^(?:a|b)c$', 'a{2,4}b', '^\\w+\\.\\w+$', '[\\w.-]+\\.com',
    'a?b', '^x{3}$', '^\\/a\\-b$',
  ])('accepts %j', pattern => expect(safeRegex(pattern), pattern).toBeInstanceOf(RegExp))

  it('refuses more than two unbounded quantifiers, and a polynomial pattern comes back at once, undecided', () => {
    const poly = '[a-z.]*'.repeat(8) + '!'
    expect(safeRegex(poly)).toBeNull()
    expect(safeRegex('a+b+c+')).toBeNull()
    expect(safeRegex('a{1,}b*c{2,}')).toBeNull()
    expect(safeRegex('a+b+')).toBeInstanceOf(RegExp)
    const host = 'a'.repeat(52)
    const t0 = performance.now()
    expect(ev(`DOMAIN-REGEX,${poly},X`, { address: host + '.com' })).toEqual(unknown('regex'))
    expect(performance.now() - t0).toBeLessThan(50)
  })

  it('a host longer than a domain name can be is not an address', () => {
    expect(prepareProbe(input({ address: 'a'.repeat(254) })).ok).toBe(false)
    expect(prepareProbe(input({ address: ('a'.repeat(60) + '.').repeat(4) + 'com' })).ok).toBe(true)
  })
})

describe('a match is not always the last word (review D)', () => {
  const groups = new Map([
    ['Udp', [{ kind: 'builtin' as const, value: 'DIRECT' }, { kind: 'builtin' as const, value: 'PASS' }]],
    ['YT', [{ kind: 'builtin' as const, value: 'DIRECT' }]],
    ['B', [{ kind: 'builtin' as const, value: 'DIRECT' }]],
  ])
  const real = continuation(groups, ['R'])
  const doc = parseDoc('- NETWORK,udp,Udp\n- DOMAIN-SUFFIX,youtube.com,YT\n- MATCH,B\n')

  it('the repro: UDP to youtube.com meets a group that may pass, so the answer is not certain and YT may apply', () => {
    const r = routeFor(doc, probe({ address: 'youtube.com', network: 'udp' }), real)
    expect(r.first).toEqual({ line: 0, target: 'Udp' })
    expect(r.final).toBe(false)
    expect(r.certain).toBe(false)
    expect(r.later.map(l => l.line)).toEqual([1, 2])
  })

  it('over TCP the first rule does not match and the YT group, which cannot pass, is final', () => {
    const r = routeFor(doc, probe({ address: 'youtube.com', network: 'tcp' }), real)
    expect(r.first).toEqual({ line: 1, target: 'YT' })
    expect(r.final).toBe(true)
    expect(r.certain).toBe(true)
    expect(r.later.map(l => l.line)).toEqual([2])
  })

  it('a built-in outbound is final; a group the data does not know is not; a Rematch outbound is never', () => {
    expect(routeFor(parseDoc('- MATCH,DIRECT\n'), probe(), real).final).toBe(true)
    expect(routeFor(parseDoc('- MATCH,Mystery\n'), probe(), real).final).toBe(false)
    expect(routeFor(parseDoc('- MATCH,R\n'), probe(), real).final).toBe(false)
    expect(routeFor(parseDoc('- MATCH,Mystery\n'), probe(), continuation(null, [])).certain).toBe(false)
  })

  it('a first rule that is not final is not "certain" even with nothing undecided above it', () => {
    const r = routeFor(parseDoc('- MATCH,Udp\n'), probe(), real)
    expect(r.maybeTotal).toBe(0)
    expect(r.certain).toBe(false)
  })
})

describe('no-resolve after a rule that resolved the name', () => {
  const doc = parseDoc('- IP-CIDR,1.0.0.0/8,X\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n- MATCH,F\n')

  it('is listed as one that may match: by then Mihomo has the address', () => {
    const r = routeFor(doc, probe(), FINALS)
    expect(r.maybe.map(m => [m.line, m.reason])).toEqual([[0, 'resolve'], [1, 'resolve']])
  })

  it('with no address entered and no earlier rule resolving, it is not decided (the connection may already hold an address)', () => {
    const r = routeFor(parseDoc('- IP-CIDR,10.0.0.0/8,Y,no-resolve\n- MATCH,F\n'), probe(), FINALS)
    expect(r.maybe.map(m => [m.line, m.reason])).toEqual([[0, 'input']])
    expect(r.first).toEqual({ line: 1, target: 'F' })
    expect(r.certain).toBe(false)
  })

  it('stays a plain miss when an address was entered that the rule does not cover and no earlier rule resolves', () => {
    const r = routeFor(parseDoc('- IP-CIDR,10.0.0.0/8,Y,no-resolve\n- MATCH,F\n'), probe({ resolvedIp: '11.1.1.1' }), FINALS)
    expect(r.maybe).toEqual([])
    expect(r.first).toEqual({ line: 1, target: 'F' })
  })

  it('with the resolved address entered it is decided: it is known once an earlier rule resolved it', () => {
    const r = routeFor(doc, probe({ resolvedIp: '10.1.1.1' }), FINALS)
    expect(r.first).toEqual({ line: 1, target: 'Y' })
    expect(r.certain).toBe(true)
  })
})

describe('regexes that backtrack, and what regexp2 will not compile (re-review)', () => {
  it('counts every quantifier that can repeat or be skipped: bounded ones too, and a bound above 64', () => {
    const bounded = '.{0,60}'.repeat(4) + 'z'
    const optional = 'a?'.repeat(32) + 'a'.repeat(32)
    for (const bad of [bounded, optional, 'a{0,100}b', 'a{65}', 'a{1,2}b{1,2}c{1,2}', 'a?b?c?']) expect(safeRegex(bad), bad).toBeNull()
    for (const good of ['a?b?', 'a{2,4}b', 'a{64}', '^(www\\.)?google\\.(com|cn)$', '^\\w+\\.\\w+$']) expect(safeRegex(good), good).toBeInstanceOf(RegExp)
  })

  it('the three patterns come back undecided at once, even on a 250-character host', () => {
    const host = 'a'.repeat(250)
    for (const pattern of ['.{0,60}'.repeat(4) + 'z', 'a?'.repeat(32) + 'a'.repeat(32), '[a-z.]*'.repeat(8) + '!']) {
      const t0 = performance.now()
      expect(ev(`DOMAIN-REGEX,${pattern},X`, { address: host })).toEqual(unknown('regex'))
      expect(performance.now() - t0).toBeLessThan(50)
    }
  })

  it('a class range that starts or ends with an escape class is refused (regexp2 does not compile it)', () => {
    for (const bad of ['[a-\\d]', '[\\d-z]', '[\\w-a]', '[0-\\s]']) expect(safeRegex(bad), bad).toBeNull()
    for (const good of ['[a-z\\d-]', '[\\w.-]', '[-a-z]', '[a\\-z]']) expect(safeRegex(good), good).toBeInstanceOf(RegExp)
  })

  it('a host with a non-ASCII character is not evaluated: the punycode form is asked for', () => {
    expect(prepareProbe(input({ address: 'bücher.example' }))).toEqual({ ok: false, problem: 'address_idn' })
    expect(prepareProbe(input({ address: '例え.jp' }))).toEqual({ ok: false, problem: 'address_idn' })
    expect(prepareProbe(input({ address: 'xn--bcher-kva.example' })).ok).toBe(true)
  })
})

describe('UDP over a group with proxies in it (re-review)', () => {
  const groups = new Map([
    ['Pool', [{ kind: 'builtin' as const, value: 'DIRECT' }, { kind: 'node_set' as const, value: 'remaining' }]],
    ['Plain', [{ kind: 'builtin' as const, value: 'DIRECT' }]],
  ])
  const real = continuation(groups, [])
  const doc = parseDoc('- DOMAIN-SUFFIX,a.example,Pool\n- MATCH,Plain\n')

  it('over TCP the group is the last word; over UDP Mihomo skips a chosen proxy that lacks UDP and goes on, so it is not', () => {
    expect(routeFor(doc, probe({ address: 'a.example', network: 'tcp' }), real).final).toBe(true)
    const udp = routeFor(doc, probe({ address: 'a.example', network: 'udp' }), real)
    expect(udp.final).toBe(false)
    expect(udp.certain).toBe(false)
  })

  it('a group of built-in outbounds only stays final over UDP', () => {
    expect(routeFor(parseDoc('- MATCH,Plain\n'), probe({ network: 'udp' }), real).final).toBe(true)
  })
})
