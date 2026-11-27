import { describe, expect, it } from 'vitest'
import { buildBlocks } from './blocks'
import { reviewPaste, scanPaste } from './detect'
import { parseDoc } from './parse'

const one = (line: string, bare?: 'DOMAIN-SUFFIX' | 'DOMAIN') => scanPaste(line, bare)

describe('what a pasted line is (design 3.6)', () => {
  it.each([
    ['DOMAIN-SUFFIX,example.com,Anything', 'DOMAIN-SUFFIX', 'example.com', []],
    ['- DOMAIN-SUFFIX,example.com,Anything', 'DOMAIN-SUFFIX', 'example.com', []],
    ['  - DOMAIN,www.example.com,X', 'DOMAIN', 'www.example.com', []],
    ['DOMAIN-SUFFIX,example.com', 'DOMAIN-SUFFIX', 'example.com', []],
    ['domain-keyword,ads,X', 'DOMAIN-KEYWORD', 'ads', []],
    ['IP-CIDR,1.2.3.0/24,X,no-resolve', 'IP-CIDR', '1.2.3.0/24', ['no-resolve']],
    ['IP-CIDR,1.2.3.0/24,X', 'IP-CIDR', '1.2.3.0/24', []],
    ['example.com', 'DOMAIN-SUFFIX', 'example.com', []],
    ['+.example.com', 'DOMAIN-SUFFIX', 'example.com', []],
    ['.example.com', 'DOMAIN-SUFFIX', 'example.com', []],
    ['||example.com^', 'DOMAIN-SUFFIX', 'example.com', []],
    ['0.0.0.0 ads.example.com', 'DOMAIN-SUFFIX', 'ads.example.com', []],
    ['127.0.0.1\tads.example.com', 'DOMAIN-SUFFIX', 'ads.example.com', []],
    ["  - '+.example.com'", 'DOMAIN-SUFFIX', 'example.com', []],
    ['1.2.3.4', 'IP-CIDR', '1.2.3.4/32', ['no-resolve']],
    ['1.2.3.0/24', 'IP-CIDR', '1.2.3.0/24', ['no-resolve']],
    ['2001:db8::1', 'IP-CIDR6', '2001:db8::1/128', ['no-resolve']],
    ['2001:db8::/32', 'IP-CIDR6', '2001:db8::/32', ['no-resolve']],
  ])('%j', (line, type, value, options) => {
    const { entries, unrecognized } = one(line)
    expect(unrecognized).toEqual([])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ type, value, options })
  })

  it('takes a bare domain as the type the dialog says', () => {
    expect(one('example.com', 'DOMAIN').entries[0]).toMatchObject({ type: 'DOMAIN', value: 'example.com' })
  })

  it('marks the guesses that change meaning', () => {
    expect(one('*.example.com').entries[0]).toMatchObject({ value: 'example.com', note: 'wildcard' })
    expect(one('https://www.example.com/path?q=1').entries[0]).toMatchObject({ type: 'DOMAIN-SUFFIX', value: 'www.example.com', note: 'url_host' })
  })

  it('counts comments and blank lines, and skips a provider header', () => {
    const r = scanPaste('payload:\n# c\n\n  \nexample.com\n// x? \n')
    expect(r.entries.map(e => e.value)).toEqual(['example.com'])
    expect(r.comments).toBe(3)
    expect(r.unrecognized.map(u => u.text)).toEqual(['// x?'])
  })

  it('counts rule lines that named another target', () => {
    const r = scanPaste('DOMAIN,a.example,X\nDOMAIN,b.example\nIP-CIDR,1.2.3.0/24,Y,no-resolve\n')
    expect(r.ignoredTarget).toBe(2)
  })

  it('cannot add what it cannot read as a matcher', () => {
    const r = scanPaste('AND,((NETWORK,UDP),(DST-PORT,443)),X\nMATCH,X\nDOMIAN,a.example,X\nnot a domain\n999.1.1.1\n')
    expect(r.entries).toEqual([])
    expect(r.unrecognized).toHaveLength(5)
    expect(r.unrecognized[0]).toEqual({ line: 1, text: 'AND,((NETWORK,UDP),(DST-PORT,443)),X' })
  })
})

describe('reviewing a paste against the document', () => {
  const doc = parseDoc('- DOMAIN-SUFFIX,a.example,X\n- DOMAIN-SUFFIX,b.example,X\n# s\n- DOMAIN-SUFFIX,c.example,Y\n- DOMAIN-SUFFIX,d.example,Z\n')
  const blocks = buildBlocks(doc)

  it('skips what the group or the paste already has, matching domains without regard to case', () => {
    const { entries } = scanPaste('a.example\nnew.example\nNEW.example\nB.example\n')
    const r = reviewPaste(entries, doc, blocks, { kind: 'into', block: 0 })
    expect(r.add.map(e => e.value)).toEqual(['new.example'])
    expect(r.duplicateInGroup).toBe(2)
    expect(r.duplicateInPaste).toBe(1)
  })

  it('says when an earlier group already has it, so the new entry would never apply', () => {
    const { entries } = scanPaste('a.example\n')
    const r = reviewPaste(entries, doc, blocks, { kind: 'into', block: 1 })
    expect(r.earlier).toEqual([{ value: 'a.example', block: 0 }])
    expect(r.add).toHaveLength(1)
  })

  it('says when a later group has it, which the new entry would take over', () => {
    const { entries } = scanPaste('c.example\nd.example\n')
    const r = reviewPaste(entries, doc, blocks, { kind: 'into', block: 0 })
    expect(r.later).toEqual([{ block: 1, count: 1 }, { block: 2, count: 1 }])
  })

  it('judges a new group by where it will sit', () => {
    const { entries } = scanPaste('c.example\n')
    expect(reviewPaste(entries, doc, blocks, { kind: 'new', before: 1 }).later).toEqual([{ block: 1, count: 1 }])
    expect(reviewPaste(entries, doc, blocks, { kind: 'new', before: 2 }).earlier).toEqual([{ value: 'c.example', block: 1 }])
  })
})

describe('reading a bare domain', () => {
  it('answers at once for a long run of `!.` (no catastrophic backtracking)', () => {
    const started = performance.now()
    const scan = scanPaste('a' + '!.'.repeat(120) + ' ')
    expect(performance.now() - started).toBeLessThan(100)
    expect(scan.entries).toHaveLength(0)
  })

  it('still reads ordinary domains and refuses an empty label', () => {
    expect(scanPaste('example.com\nsub.example.co.uk').entries.map(e => e.value)).toEqual(['example.com', 'sub.example.co.uk'])
    expect(scanPaste('a..b.com').entries).toHaveLength(0)
  })
})
