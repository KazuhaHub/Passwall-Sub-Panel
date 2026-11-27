import { describe, expect, it } from 'vitest'
import { defaultRulesContent } from './defaultRules.fixture'
import { continuation, type Continuation } from './continuation'
import { findShadowed, summarize, type Finding } from './lint'
import { parseDoc } from './parse'

const D = parseDoc
/** Only PASS goes on, as before; the tests about groups that may pass bring their own. */
const FINALS: Continuation = { mayContinue: t => t.toUpperCase() === 'PASS' }
const find = (text: string, cont: Continuation = FINALS) => findShadowed(D(text), cont)
const brief = (f: Finding[]) => f.map(x => [x.line, x.by, x.kind])

describe('rules an earlier rule already covers', () => {
  it('an exact duplicate: harmless with the same target, never applies with another', () => {
    expect(brief(find('- DOMAIN,a.com,X\n- DOMAIN,a.com,X\n- DOMAIN,a.com,Y\n'))).toEqual([[1, 0, 'same'], [2, 0, 'other']])
    expect(find('- DOMAIN,a.com,X\n- DOMAIN,A.COM,X\n')[0].duplicate).toBe(true)
  })

  it('a DOMAIN under an earlier DOMAIN-SUFFIX, at any depth, and the suffix itself', () => {
    expect(brief(find('- DOMAIN-SUFFIX,example.com,X\n- DOMAIN,www.example.com,X\n- DOMAIN,a.b.example.com,Y\n- DOMAIN,example.com,X\n- DOMAIN,notexample.com,X\n'))).toEqual([[1, 0, 'same'], [2, 0, 'other'], [3, 0, 'same']])
  })

  it('a DOMAIN-SUFFIX under a broader earlier DOMAIN-SUFFIX, but not the other way round', () => {
    expect(brief(find('- DOMAIN-SUFFIX,example.com,X\n- DOMAIN-SUFFIX,www.example.com,Y\n'))).toEqual([[1, 0, 'other']])
    expect(find('- DOMAIN-SUFFIX,www.example.com,X\n- DOMAIN-SUFFIX,example.com,X\n')).toEqual([])
    expect(find('- DOMAIN,example.com,X\n- DOMAIN-SUFFIX,example.com,X\n')).toEqual([])
  })

  it('a keyword covers what contains it: a later keyword that contains it, a DOMAIN or DOMAIN-SUFFIX that contains it', () => {
    expect(brief(find('- DOMAIN-KEYWORD,goog,X\n- DOMAIN-KEYWORD,google,X\n- DOMAIN-SUFFIX,google.com,Y\n- DOMAIN,www.google.com,X\n- DOMAIN-SUFFIX,go.com,X\n'))).toEqual([[1, 0, 'same'], [2, 0, 'other'], [3, 0, 'same']])
    // The keyword cuts across the dot, so the suffix `x.com` (which matches `x.com` itself) is not covered by `.x`.
    expect(find('- DOMAIN-KEYWORD,.x,X\n- DOMAIN-SUFFIX,x.com,X\n')).toEqual([])
  })

  it('reports the FIRST rule that covers', () => {
    expect(brief(find('- DOMAIN-SUFFIX,com,X\n- DOMAIN-SUFFIX,example.com,Y\n- DOMAIN-SUFFIX,www.example.com,Y\n'))).toEqual([[1, 0, 'other'], [2, 0, 'other']])
  })

  it('a PASS rule covers nothing: matching goes on past it', () => {
    expect(find('- DOMAIN-SUFFIX,example.com,PASS\n- DOMAIN,www.example.com,X\n')).toEqual([])
    expect(brief(find('- DOMAIN-SUFFIX,example.com,pass\n- DOMAIN,www.example.com,X\n- DOMAIN,www.example.com,Y\n'))).toEqual([[2, 1, 'other']])
  })

  it('a rule with no target does not cover and is not reported', () => {
    expect(find('- DOMAIN-SUFFIX,example.com\n- DOMAIN,www.example.com,X\n- DOMAIN,www.example.com\n')).toEqual([])
  })

  it('an address range under a wider earlier one, mind no-resolve and the two families', () => {
    expect(brief(find('- IP-CIDR,10.0.0.0/8,X,no-resolve\n- IP-CIDR,10.1.0.0/16,Y,no-resolve\n- IP-CIDR,11.0.0.0/8,Y\n- IP-CIDR,10.0.0.0/8,X,no-resolve\n'))).toEqual([[1, 0, 'other'], [3, 0, 'same']])
    // A wide rule that resolves covers a narrower one that does not; the other way round it does not (the resolving one also matches resolved names).
    expect(brief(find('- IP-CIDR,10.0.0.0/8,X\n- IP-CIDR,10.1.0.0/16,X,no-resolve\n'))).toEqual([[1, 0, 'same']])
    expect(find('- IP-CIDR,10.0.0.0/8,X,no-resolve\n- IP-CIDR,10.1.0.0/16,X\n')).toEqual([])
    expect(brief(find('- IP-CIDR6,fc00::/7,X,no-resolve\n- IP-CIDR6,fd00::/8,Y,no-resolve\n- IP-CIDR,0.0.0.0/0,X,no-resolve\n- IP-CIDR6,::/0,X,no-resolve\n'))).toEqual([[1, 0, 'other']])
    // A source-address rule is a different question.
    expect(find('- IP-CIDR,10.0.0.0/8,X,no-resolve\n- IP-CIDR,10.1.0.0/16,X,no-resolve,src\n')).toEqual([])
  })

  it('options are exact, as in Mihomo: NO-RESOLVE is not no-resolve, and SRC is not src', () => {
    // The wide rule resolves (its option is not recognised), so it covers the narrow no-resolve one; the reverse does not hold.
    expect(brief(find('- IP-CIDR,10.0.0.0/8,X,NO-RESOLVE\n- IP-CIDR,10.1.0.0/16,X,no-resolve\n'))).toEqual([[1, 0, 'same']])
    expect(find('- IP-CIDR,10.0.0.0/8,X,no-resolve\n- IP-CIDR,10.1.0.0/16,X,NO-RESOLVE\n')).toEqual([])
    // `SRC` does not turn the rule into a source rule, so it is a destination range that covers the narrower one.
    expect(brief(find('- IP-CIDR,10.0.0.0/8,X,SRC\n- IP-CIDR,10.1.0.0/16,Y,SRC\n'))).toEqual([[1, 0, 'other']])
    // A different spelling of an option is a different rule, not a duplicate.
    expect(find('- IP-CIDR,10.0.0.0/8,X,no-resolve\n- IP-CIDR,10.0.0.0/8,X,NO-RESOLVE\n')).toEqual([])
  })

  it('a prefix Mihomo refuses (a leading zero) covers nothing', () => {
    expect(find('- IP-CIDR,010.0.0.0/8,X\n- IP-CIDR,10.1.0.0/16,X\n')).toEqual([])
  })

  it('other types are only compared for exact duplicates', () => {
    expect(brief(find('- NETWORK,udp,X\n- NETWORK,udp,Y\n- DST-PORT,443,X\n- DST-PORT,80-90,X\n- GEOIP,CN,X\n- GEOIP,CN,X,no-resolve\n- GEOIP,cn,X\n'))).toEqual([[1, 0, 'other']])
    expect(brief(find('- AND,((NETWORK,UDP),(DST-PORT,443)),X\n- AND,((NETWORK,UDP),(DST-PORT,443)),Y\n'))).toEqual([[1, 0, 'other']])
  })

  it('everything after the fallback never matches', () => {
    expect(brief(find('- DOMAIN,a.com,X\n- MATCH,F\n- DOMAIN,b.com,X\n# c\n- DOMAIN-SUFFIX,c.com,Y\n'))).toEqual([[2, 1, 'after_match'], [4, 1, 'after_match']])
  })

  it('a MATCH that names PASS does not end the list', () => {
    expect(find('- MATCH,PASS\n- DOMAIN,b.com,X\n')).toEqual([])
  })

  it('comments, blanks and lines read as text are ignored; a document with a foreign line is not analysed', () => {
    expect(find('# a\n\n- \'DOMAIN,a.com,X\'\n- DOMAIN,a.com,X\n')).toEqual([])
    expect(find('- DOMAIN,a.com,X\n- DOMAIN,a.com,X\n  ,DIRECT\n')).toEqual([])
  })
})

describe('a rule that may not be the last word covers nothing (review D)', () => {
  const real = continuation(new Map([
    ['Udp', [{ kind: 'builtin' as const, value: 'DIRECT' }, { kind: 'builtin' as const, value: 'PASS' }]],
    ['YT', [{ kind: 'builtin' as const, value: 'DIRECT' }]],
  ]), ['R'])

  it('a suffix aimed at a group that may pass does not shadow the rules below it', () => {
    expect(find('- DOMAIN-SUFFIX,youtube.com,Udp\n- DOMAIN,www.youtube.com,YT\n', real)).toEqual([])
    expect(brief(find('- DOMAIN-SUFFIX,youtube.com,YT\n- DOMAIN,www.youtube.com,Udp\n', real))).toEqual([[1, 0, 'other']])
  })

  it('nor does one aimed at a Rematch outbound, or at a group the data does not list', () => {
    expect(find('- DOMAIN-SUFFIX,a.com,R\n- DOMAIN,www.a.com,YT\n', real)).toEqual([])
    expect(find('- DOMAIN-SUFFIX,a.com,Mystery\n- DOMAIN,www.a.com,YT\n', real)).toEqual([])
  })

  it('a MATCH that may pass does not make the rules after it unreachable', () => {
    expect(find('- MATCH,Udp\n- DOMAIN,b.com,YT\n', real)).toEqual([])
    expect(brief(find('- MATCH,YT\n- DOMAIN,b.com,YT\n', real))).toEqual([[1, 0, 'after_match']])
  })

  it('an address range aimed at such a group does not cover a narrower one', () => {
    expect(find('- IP-CIDR,10.0.0.0/8,Udp,no-resolve\n- IP-CIDR,10.1.0.0/16,YT,no-resolve\n', real)).toEqual([])
  })
})

describe('the summary', () => {
  it('counts the three kinds', () => {
    const f = find('- DOMAIN,a.com,X\n- DOMAIN,a.com,X\n- DOMAIN,a.com,Y\n- MATCH,F\n- DOMAIN,z.com,X\n')
    expect(summarize(f)).toEqual({ same: 1, other: 1, afterMatch: 1, total: 3 })
  })
})

// The same facts, by brute force over every pair: the trie and the buckets must agree with what the definitions say.
const DOMAIN_TYPES = new Set(['DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD'])
function covers(e: { type: string; value: string }, l: { type: string; value: string }): boolean {
  const ev = e.value.toLowerCase()
  const lv = l.value.toLowerCase()
  if (e.type === 'DOMAIN') return l.type === 'DOMAIN' && lv === ev
  if (e.type === 'DOMAIN-SUFFIX') return (l.type === 'DOMAIN' || l.type === 'DOMAIN-SUFFIX') && (lv === ev || lv.endsWith('.' + ev))
  if (e.type === 'DOMAIN-KEYWORD') return lv.includes(ev)
  return false
}

describe('the seeded default rule set', () => {
  const content = defaultRulesContent()
  const doc = parseDoc(content)
  const found = findShadowed(doc, FINALS)
  const domain = found.filter(f => DOMAIN_TYPES.has(doc.lines[f.line].rule!.type.toUpperCase()))

  it('finds the shadowed domain rules the design counted, and the copilot example', () => {
    expect(domain.length).toBe(1466)
    // The design counted 1,140 / 326 by probing each rule's own text; this classifies by the first rule that covers the whole
    // rule, which moves six rules (a suffix whose first coverer is a keyword with another target) from "same" to "other".
    expect(domain.filter(f => f.kind === 'same').length).toBe(1134)
    expect(domain.filter(f => f.kind === 'other').length).toBe(332)
    const copilot = found.find(f => doc.lines[f.line].raw === '- DOMAIN-SUFFIX,copilot.microsoft.com,💬 Ai平台')!
    expect(copilot.kind).toBe('other')
    expect(doc.lines[copilot.by].raw).toBe('- DOMAIN-SUFFIX,copilot.microsoft.com,Ⓜ️ 微软Bing')
  })

  it('agrees with a pair-by-pair check on every rule it reports and on a sample of those it does not', () => {
    const rules = doc.lines.map(l => (l.kind === 'rule' ? { type: l.rule!.type.toUpperCase(), value: l.rule!.value, target: l.rule!.target } : null))
    const byLine = new Map(domain.map(f => [f.line, f]))
    const wrong: string[] = []
    for (const f of domain) {
      const l = rules[f.line]!
      if (!covers(rules[f.by]!, l)) wrong.push(`${f.line} is not covered by ${f.by}`)
      // `by` is the first rule that covers.
      for (let i = 0; i < f.by; i++) { const e = rules[i]; if (e && DOMAIN_TYPES.has(e.type) && covers(e, l)) wrong.push(`${i} covers ${f.line} before ${f.by}`) }
    }
    let checked = 0
    for (let i = 0; i < rules.length && checked < 400; i += 23) {
      const l = rules[i]
      if (!l || !DOMAIN_TYPES.has(l.type) || byLine.has(i)) continue
      checked++
      for (let j = 0; j < i; j++) { const e = rules[j]; if (e && DOMAIN_TYPES.has(e.type) && covers(e, l)) wrong.push(`${j} covers ${i}, which was not reported`) }
    }
    expect(wrong).toEqual([])
    expect(checked).toBeGreaterThan(300)
  })

  it('is fast: the whole set in well under a second', () => {
    const t0 = performance.now()
    findShadowed(doc, FINALS)
    expect(performance.now() - t0).toBeLessThan(500)
  })

  it('a rule set of 50,000 rules is analysed in well under a second', () => {
    const lines: string[] = []
    for (let i = 0; i < 50000; i++) lines.push(i % 7 === 0 ? `- IP-CIDR,${(i >> 8) & 255}.${i & 255}.0.0/16,T${i % 5},no-resolve` : `- DOMAIN-SUFFIX,h${i % 20000}.d${i % 13}.example,T${i % 5}`)
    const big = parseDoc(lines.join('\n') + '\n')
    const t0 = performance.now()
    findShadowed(big, FINALS)
    expect(performance.now() - t0).toBeLessThan(1000)
  })
})


describe('an identical rule with the same target is redundant even where the target may go on (re-review)', () => {
  const real = continuation(new Map([['Udp', [{ kind: 'builtin' as const, value: 'DIRECT' }, { kind: 'builtin' as const, value: 'PASS' }]]]), [])
  it('the same rule again, aimed at the same group that may pass: the second can add nothing', () => {
    expect(brief(find('- DOMAIN,a.com,Udp\n- DOMAIN,a.com,Udp\n', real))).toEqual([[1, 0, 'same']])
  })
  it('aimed at another target it can still apply, so it is not reported', () => {
    expect(find('- DOMAIN,a.com,Udp\n- DOMAIN,a.com,YT\n', real)).toEqual([])
  })
  it('without any group data the findings that do not depend on it remain: a duplicate, and coverage by a built-in outbound', () => {
    const none = continuation(null, [])
    expect(brief(find('- DOMAIN,a.com,G\n- DOMAIN,a.com,G\n- DOMAIN-SUFFIX,b.com,DIRECT\n- DOMAIN,www.b.com,REJECT\n- DOMAIN,c.com,G\n- DOMAIN,c.com,H\n', none))).toEqual([[1, 0, 'same'], [3, 2, 'other']])
  })
})
