import { describe, expect, it } from 'vitest'
import { defaultRulesContent } from './defaultRules.fixture'
import { parseDoc, parseLine, serializeDoc, splitFields } from './parse'

const round = (s: string) => serializeDoc(parseDoc(s))

describe('lossless round trip (invariant A)', () => {
  it('returns the seeded default rule set byte for byte', () => {
    const text = defaultRulesContent()
    expect(text.split('\n').length).toBeGreaterThan(9000)
    expect(round(text)).toBe(text)
  })

  const nasty: [string, string][] = [
    ['empty', ''],
    ['one newline', '\n'],
    ['only blanks', '\n\n  \n'],
    ['no trailing newline', '- DOMAIN,a.example,X'],
    ['trailing newline', '- DOMAIN,a.example,X\n'],
    ['CRLF', '- DOMAIN,a.example,X\r\n- DOMAIN,b.example,X\r\n'],
    ['mixed line endings', '- DOMAIN,a.example,X\r\n- DOMAIN,b.example,X\n# c\r\n'],
    ['bare CR inside', 'a\rb\n'],
    ['BOM', '﻿- DOMAIN,a.example,X\n'],
    ['tabs', '\t- DOMAIN,a.example,X\n-\tDOMAIN,b.example,X\n'],
    ['odd spacing', '  -   DOMAIN-SUFFIX ,  a.example , X  ,  no-resolve   \n'],
    ['trailing whitespace', '- DOMAIN,a.example,X   \n#   note   \n   \n'],
    ['comments and unknown types', '# a\n   # b\n- DOMIAN,a.example,X\n- WHAT\n'],
    ['quoted rule', "- 'DOMAIN,a.example,X'\n- \"DOMAIN,b.example,X\"\n"],
    ['multi-line scalar', '- >\n  DOMAIN,a.example,X\n- |\n  DOMAIN,b.example,X\n'],
    ['template marker', '- DOMAIN,{{ host }},X\n'],
    ['invalid yaml', '{{{ ]]] :\n- - -\n: :\n'],
    ['not a list', 'foo: bar\n'],
    ['logical rule', '- AND,((NETWORK,UDP),(DST-PORT,443)),Quic\n'],
    ['unicode', '- DOMAIN-SUFFIX,例え.jp,🎯 全球直连\n'],
    ['long run of newlines', '\n\n\n\n'],
  ]
  it.each(nasty)('%s', (_name, text) => expect(round(text)).toBe(text))

  it('holds for random strings built from the characters that matter', () => {
    const alphabet = ['-', ' ', ',', '\n', '\r', '#', '(', ')', "'", '"', 'A', 'b', ':', '{', '\t', '日']
    let seed = 7
    const rnd = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 2 ** 32 }
    for (let i = 0; i < 400; i++) {
      let s = ''
      const n = Math.floor(rnd() * 60)
      for (let j = 0; j < n; j++) s += alphabet[Math.floor(rnd() * alphabet.length)]
      expect(round(s)).toBe(s)
    }
  })
})

describe('line kinds', () => {
  it.each([
    ['- DOMAIN,a.example,X', 'rule'],
    ['  - DOMAIN,a.example,X', 'rule'],
    ['- DOMAIN,a.example,X\r', 'rule'],
    ['# note', 'comment'],
    ['   # note', 'comment'],
    ['', 'blank'],
    ['  \r', 'blank'],
    ["- 'DOMAIN,a.example,X'", 'text'],
    ['- "DOMAIN,a.example,X"', 'text'],
    ['- DOMAIN,{{ x }},X', 'text'],
    ['- DOMAIN,a.example,X # trailing', 'text'],
    ['- key: value', 'text'],
    ['- >', 'text'],
    ['\t- DOMAIN,a.example,X', 'text'],
    ['DOMAIN,a.example,X', 'text'],
    ['-', 'text'],
    ['- - DOMAIN,a.example,X', 'text'],
  ])('%j is %s', (raw, kind) => expect(parseLine(raw).kind).toBe(kind))
})

describe('rule fields', () => {
  it('splits only top-level commas, as the backend does', () => {
    expect(splitFields('AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制')).toEqual(['AND', '((NETWORK,UDP),(DST-PORT,443))', '⚡ QUIC控制'])
    expect(splitFields('A,"b,c",D')).toEqual(['A', '"b,c"', 'D'])
  })

  it('reads type, value, target and options', () => {
    const r = parseLine('- IP-CIDR,10.0.0.0/8,🎯 全球直连,no-resolve').rule!
    expect(r).toMatchObject({ type: 'IP-CIDR', value: '10.0.0.0/8', target: '🎯 全球直连', options: ['no-resolve'], known: true, structured: true })
  })

  it('keeps the type as written and is lenient about spacing', () => {
    const r = parseLine('  -   domain-suffix ,  a.example , X  ,  src  ').rule!
    expect(r).toMatchObject({ type: 'domain-suffix', value: 'a.example', target: 'X', options: ['src'], known: true, structured: true })
  })

  // Targets follow mihomo_advanced.go ruleOutboundTargetIndex: the last field that is not blank and, past the
  // minimum position, not no-resolve / src; MATCH's target is its second field; SUB-RULE has none.
  it.each([
    ['- MATCH,Final', 'Final'],
    ['- MATCH,Final,no-resolve', 'Final'],
    ['- DOMAIN,a.example,X', 'X'],
    ['- DOMAIN,a.example,X,', 'X'],
    ['- IP-CIDR,1.2.3.0/24,X,src,no-resolve', 'X'],
    ['- IP-CIDR,1.2.3.0/24,no-resolve', 'no-resolve'],
    ['- SUB-RULE,(NETWORK,UDP),ai', ''],
    ['- AND,((NETWORK,UDP),(DST-PORT,443)),Quic', 'Quic'],
    ['- DOMAIN,a.example', ''],
    ['- WHAT', ''],
  ])('%s targets %j', (raw, target) => expect(parseLine(raw).rule!.target).toBe(target))

  it('marks what the form cannot edit as not structured', () => {
    expect(parseLine('- AND,((NETWORK,UDP),(DST-PORT,443)),Q').rule!.structured).toBe(false)
    expect(parseLine('- MATCH,Final').rule!.structured).toBe(false)
    expect(parseLine('- SUB-RULE,(A,B),ai').rule!.structured).toBe(false)
    expect(parseLine('- DOMIAN-SUFFIX,a.example,X').rule).toMatchObject({ known: false, structured: false })
    expect(parseLine('- DOMAIN-SUFFIX,a.example,X,weird').rule!.structured).toBe(false)
    expect(parseLine('- DOMAIN-SUFFIX,a.example').rule!.structured).toBe(false)
  })
})

describe('size', () => {
  // Design 3.14: a 50,000-line rule set is read in well under a second (the budget is 50 ms; this only guards a
  // collapse into something quadratic, so it is generous).
  it('reads and writes 50,000 lines quickly', () => {
    const text = Array.from({ length: 50_000 }, (_, i) => `- DOMAIN-SUFFIX,host${i}.example,Group ${i % 40}`).join('\n') + '\n'
    const t0 = performance.now()
    const doc = parseDoc(text)
    expect(serializeDoc(doc)).toBe(text)
    expect(performance.now() - t0).toBeLessThan(1500)
  })
})
