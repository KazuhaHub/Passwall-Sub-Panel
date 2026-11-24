import { describe, expect, it } from 'vitest'
import { buildBlocks } from './blocks'
import { retargetLines, renameTarget } from './ops'
import { parseDoc, parseLine, serializeDoc, targetIndex } from './parse'
import { normalizeTarget, quoteLike } from './target'

// The table mirrors internal/service/render/proxy_groups.go normalizeRulePart: trim; a part wrapped in the same quote on both
// ends is unwrapped; a double-quoted part is read as one CSV field (an error, or more than one field, leaves it as it was); a
// single-quoted part has `''` read as `'`.
describe('normalizeTarget (normalizeRulePart)', () => {
  it.each([
    ['Proxy', 'Proxy'],
    ['  Proxy  ', 'Proxy'],
    ["'Proxy'", 'Proxy'],
    ['"Proxy"', 'Proxy'],
    ["'It''s'", "It's"],
    ["'a\"b'", 'a"b'],
    ['"a""b"', 'a"b'],
    ['"a,b"', 'a,b'],
    ['"a"b"', '"a"b"'],
    ['"""', '"""'],
    ['""""', '"'],
    ['""', ''],
    ["''", ''],
    ['"', '"'],
    ["'", "'"],
    ['"abc', '"abc'],
    ["'abc", "'abc"],
    ["'a\"", "'a\""],
    ["'a'b'", "a'b"],
    ['"Pro xy"', 'Pro xy'],
    ['\'🚀 节点选择\'', '🚀 节点选择'],
  ])('%j -> %j', (raw, expected) => expect(normalizeTarget(raw)).toBe(expected))
})

describe('the target of a rule line is what the backend reads', () => {
  it('is the unquoted name, for single and double quotes', () => {
    expect(parseLine("- DOMAIN,b.com,'Proxy'").rule!.target).toBe('Proxy')
    expect(parseLine('- DOMAIN,b.com,"Proxy"').rule!.target).toBe('Proxy')
    expect(parseLine('- DOMAIN,b.com,Proxy').rule!.target).toBe('Proxy')
  })

  it('a quoted option is still an option, and does not become the target', () => {
    expect(targetIndex(['IP-CIDR', '1.2.3.4/32', 'Proxy', "'no-resolve'"])).toBe(2)
    expect(parseLine("- IP-CIDR,1.2.3.4/32,'Proxy','no-resolve'").rule!.target).toBe('Proxy')
  })

  it('Proxy, \'Proxy\' and "Proxy" are one group in the directory', () => {
    const d = parseDoc("- DOMAIN,a.com,Proxy\n- DOMAIN,b.com,'Proxy'\n- DOMAIN,c.com,\"Proxy\"\n")
    const b = buildBlocks(d)
    expect(b).toHaveLength(1)
    expect([b[0].target, b[0].count]).toEqual(['Proxy', 3])
  })
})

describe('rewriting a target keeps the line\'s quote style', () => {
  it('quoteLike writes the name inside the quotes the field had', () => {
    expect(quoteLike('Proxy', 'P2')).toBe('P2')
    expect(quoteLike("'Proxy'", 'P2')).toBe("'P2'")
    expect(quoteLike('"Proxy"', 'P2')).toBe('"P2"')
    expect(quoteLike('  "Proxy"  ', 'P2')).toBe('"P2"')
  })

  it('renameTarget rewrites all three spellings and nothing else', () => {
    const src = "- DOMAIN,a.com,Proxy\n- DOMAIN,b.com,'Proxy'\n- DOMAIN,c.com,\"Proxy\"\n- DOMAIN,d.com,Other\n"
    const r = renameTarget(parseDoc(src), 'Proxy', 'P2')
    expect(r.changed).toBe(3)
    expect(serializeDoc(r.doc)).toBe("- DOMAIN,a.com,P2\n- DOMAIN,b.com,'P2'\n- DOMAIN,c.com,\"P2\"\n- DOMAIN,d.com,Other\n")
  })

  it('retargeting a quoted line writes the new name in the same quotes, and leaves a line that already has it', () => {
    const d = parseDoc("- DOMAIN,b.com,'Proxy'\n- DOMAIN,c.com,'Other'\n")
    expect(serializeDoc(retargetLines(d, [0, 1], 'Other'))).toBe("- DOMAIN,b.com,'Other'\n- DOMAIN,c.com,'Other'\n")
  })
})
