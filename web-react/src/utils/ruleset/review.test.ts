import { describe, expect, it } from 'vitest'
import { buildBlocks } from './blocks'
import { defaultRulesContent } from './defaultRules.fixture'
import { reviewPaste, scanPaste } from './detect'
import { diffLines } from './diff'
import { deleteLines, insertRules, moveBlock, moveLinesToBlock, newBlock, replaceRange, retargetLines, rewriteRule, editRule } from './ops'
import { foreignOf, listIndent, parseDoc, parseLine, serializeDoc, splitFields } from './parse'
import { canWriteField } from './scalar'
import { validateRuleValue } from './validate'
import { newGroupNameProblem } from '../../views/admin/rulesets/targets'

const D = parseDoc
const text = (d: ReturnType<typeof D>) => serializeDoc(d)

describe('line identity survives edits (review 1)', () => {
  const src = '- DOMAIN,a,X\n- DOMAIN,b,X\n- DOMAIN,c,X\n# n\n- DOMAIN,d,Y\n'
  const ids = (d: ReturnType<typeof D>) => d.lines.map(l => l.id)

  it('gives every line its own id', () => {
    const d = D(src)
    expect(new Set(ids(d)).size).toBe(d.lines.length)
  })

  it('keeps the ids of the lines an edit did not touch, and of the line it rewrote', () => {
    const d = D(src)
    const e = editRule(d, 1, { value: 'bb' })
    expect(ids(e)).toEqual(ids(d))
  })

  it('a delete leaves the other lines their ids (so a selection by id still names the same lines)', () => {
    const d = D(src)
    const e = deleteLines(d, [1])
    expect(ids(e)).toEqual([d.lines[0].id, d.lines[2].id, d.lines[3].id, d.lines[4].id])
  })

  it('moves carry ids; new lines get new ones', () => {
    const d = D(src)
    const blocks = buildBlocks(d)
    const m = moveBlock(d, blocks, 1, 0)
    expect([...ids(m)].sort()).toEqual([...ids(d)].sort())
    const added = insertRules(d, 3, [{ type: 'DOMAIN', value: 'z' }], 'X')
    expect(new Set(ids(added)).size).toBe(ids(added).length)
    expect(ids(added)).toContain(d.lines[0].id)
  })
})

const foreign = (s: string) => parseDoc(s).foreign.map(f => [f.line, f.reason])

describe('the strict dialect: anything else is foreign (re-review 1)', () => {
  it('reads the first item as fixing the indent', () => {
    expect(listIndent(D('# c\n  - DOMAIN,a,P\n  - DOMAIN,b,Q\n'))).toBe('  ')
    expect(listIndent(D('foo\n'))).toBeNull()
  })

  // Each of these is read by the backend differently from how the editor would show it.
  it.each([
    ['a continuation that starts with a comma moves the target', '- DOMAIN,a.com,PROXY\n  ,DIRECT\n- DOMAIN,b.com,REJECT\n', [[1, 'continuation']]],
    ['a continuation after an item', '- DOMAIN,a.com,PROXY\n  .evil\n', [[1, 'continuation']]],
    ['an item whose value continues on the next line', '- DOMAIN,x.com,PROXY\n- DOMAIN,a.com,\n  DIRECT\n', [[2, 'continuation']]],
    ['a document marker', '- A\n---\n- B', [[1, 'marker']]],
    ['a tab in the indent', '\t- X', [[0, 'tab']]],
    ['a tab after the dash', '-\tX', [[0, 'tab']]],
    ['a key line and what is under it', 'rules:\n  - X', [[0, 'key']]],
    ['a first line that is not an item', 'foo\n- X', [[0, 'key']]],
    ['a block scalar header and its body', '- |\n  - DOMAIN,a,DIRECT\n- DOMAIN,b,DIRECT', [[0, 'block_scalar'], [1, 'indent']]],
    ['a folded scalar header', '- >\n  x\n', [[0, 'block_scalar'], [1, 'continuation']]],
    ['an item at another indent after a scalar', '- DOMAIN,a.com,P1\n- MATCH,Final\n  - DOMAIN-SUFFIX,x.com,Proxy\n', [[2, 'indent']]],
    ['a first item that is indented, then a shallower one', '  - DOMAIN,a,P\n- DOMAIN,b,Q\n', [[1, 'indent']]],
    ['a Unicode line break inside a line', 'foo\u2028- x', [[0, 'linebreak']]],
    ['an empty item', '- DOMAIN,a,P\n-\n', [[1, 'empty_item']]],
  ])('%s', (_n, text, expected) => expect(foreign(text as string)).toEqual(expected))

  it.each([
    '', '\n', '# c\n  - DOMAIN,a,P\n  - DOMAIN,b,Q\n', '- DOMAIN,a,P\n# x\n\n   # y\n- DOMAIN,b,Q\n', '- DOMAIN,a,P\r\n- DOMAIN,b,Q\r\n',
    "- 'quoted,rule'\n- DOMAIN,{{ x }},P\n",
  ])('a plain document has no foreign line: %j', text => expect(foreign(text)).toEqual([]))

  it('the seeded default rule set has none', () => {
    expect(foreignOf(parseDoc(defaultRulesContent()).lines)).toEqual([])
  })

  it('is re-read after an edit: deleting the first item changes which indent is the list\'s', () => {
    const d = D('- DOMAIN,a,P\n  - DOMAIN,b,Q\n')
    expect(d.foreign).toHaveLength(1)
    expect(deleteLines(d, [0]).foreign).toEqual([])
  })

  it('writes pasted rules at the document indent, wherever the snippet was indented', () => {
    const d = D('  - DOMAIN,a,P\n')
    expect(text(insertRules(d, 1, [{ type: 'DOMAIN', value: 'b' }], 'P'))).toBe('  - DOMAIN,a,P\n  - DOMAIN,b,P\n')
    expect(text(newBlock(d, buildBlocks(d), 1, 'Q', [{ type: 'DOMAIN', value: 'c' }]))).toBe('  - DOMAIN,a,P\n  - DOMAIN,c,Q\n')
  })

  it('edits as text are brought to the document indent', () => {
    const d = D('- DOMAIN,a,P\n- DOMAIN,b,P\n')
    expect(text(replaceRange(d, 1, 2, '    - DOMAIN,x,P\n    - DOMAIN,y,P'))).toBe('- DOMAIN,a,P\n- DOMAIN,x,P\n- DOMAIN,y,P\n')
  })

  it('skips a `rules:` header when pasting a Clash snippet', () => {
    const r = scanPaste('rules:\n  - DOMAIN-SUFFIX,a.example,P\n  - IP-CIDR,1.2.3.0/24,P,no-resolve\n')
    expect(r.entries.map(e => e.value)).toEqual(['a.example', '1.2.3.0/24'])
    expect(r.unrecognized).toEqual([])
  })
})

describe('no target is not a target (review 3)', () => {
  it('runs of lines with no outbound are their own kind of group', () => {
    const d = D('- SUB-RULE,(NETWORK,UDP),sub1\n- SUB-RULE,(NETWORK,TCP),sub1\n- DOMAIN,a,X\n- DOMAIN,b\n')
    const b = buildBlocks(d)
    expect(b.map(x => [x.kind, x.target, x.count])).toEqual([['notarget', '', 2], ['rules', 'X', 1], ['notarget', '', 1]])
  })

  it('refuses to write or aim a rule at an empty target', () => {
    const d = D('- DOMAIN,a,X\n')
    expect(() => insertRules(d, 1, [{ type: 'DOMAIN', value: 'x.com' }], '')).toThrow()
    expect(() => retargetLines(d, [0], '')).toThrow()
    expect(() => newBlock(d, buildBlocks(d), 0, '', [{ type: 'DOMAIN', value: 'x' }])).toThrow()
    expect(() => moveLinesToBlock(D('- DOMAIN,a,X\n- SUB-RULE,(A,B),s\n'), buildBlocks(D('- DOMAIN,a,X\n- SUB-RULE,(A,B),s\n')), [0], 1)).toThrow()
  })

  it('leaves a line that has no target field alone when retargeting', () => {
    expect(rewriteRule('- SUB-RULE,(A,B),s', { target: 'Q' })).toBe('- SUB-RULE,(A,B),s')
  })
})

describe('what can be written as a field (review 4)', () => {
  const bad: [string, string][] = [
    ['foo\t#bar', 'comment'], ['a:\tb', 'colon'], ['foo\u2028- x', 'newline'], ['a\u0085b', 'newline'], ['\u0001', 'control'], ['a\u007fb', 'control'], ['a\tb', 'control'],
    ['', 'empty'], ['  ', 'empty'], ['a b # ads', 'comment'], ['x #y', 'comment'], ['a: b', 'colon'], ['a:', 'colon'], ["it's", 'quote'], ['"q"', 'quote'], ['a"b', 'quote'],
    ['{{ x }}', 'template'], ['[a]', 'indicator'], ['{a}', 'indicator'], ['- a', 'indicator'], ['!a', 'indicator'], ['&a', 'indicator'], ['*a', 'indicator'], ['|a', 'indicator'],
    ['>a', 'indicator'], ['%a', 'indicator'], ['@a', 'indicator'], ['`a', 'indicator'], ['a(b', 'parens'], ['a,b', 'comma'], ['a\nb', 'newline'], [' a', 'space_edge'], ['a ', 'space_edge'],
  ]
  it.each(bad)('rejects %j as %s', (v, why) => expect(canWriteField(v)).toBe(why))

  it.each(['example.com', 'a.b-c.example', '10.0.0.0/8', 'fc00::/7', '🎯 全球直连', 'Ⓜ️ 微软Bing', 'a(b)c', '^a\\.example$', 'x#y', 'com.viu.pad', '1000-2000'])('accepts %j', v => {
    expect(canWriteField(v)).toBeNull()
  })

  it('what it accepts survives the backend splitting and the plain-scalar reading, as written', () => {
    for (const v of ['example.com', '^a\\.example$', 'a(b)c', 'x#y', '🎯 全球直连', '1000-2000']) {
      const raw = `- DOMAIN-REGEX,${v},Group A`
      const line = parseLine(raw)
      expect(line.kind).toBe('rule')
      expect(splitFields(raw.slice(2))).toEqual(['DOMAIN-REGEX', v, 'Group A'])
      expect(line.rule).toMatchObject({ value: v, target: 'Group A' })
    }
  })

  it('formatting a rule with a value that would break the line throws instead of writing it', () => {
    const d = D('- DOMAIN,a,X\n')
    expect(() => insertRules(d, 1, [{ type: 'DOMAIN-SUFFIX', value: 'example.com # ads' }], 'X')).toThrow()
    expect(() => insertRules(d, 1, [{ type: 'PROCESS-NAME', value: "it's" }], 'X')).toThrow()
    expect(() => insertRules(d, 1, [{ type: 'DOMAIN', value: 'a' }], 'X # y')).toThrow()
  })

  it('paste strips a trailing comment and says so', () => {
    const r = scanPaste('DOMAIN-SUFFIX,example.com # ads\nexample.org # more\n')
    expect(r.entries.map(e => [e.value, e.note])).toEqual([['example.com', 'comment'], ['example.org', 'comment']])
  })

  it('paste rejects what cannot be written, per line, with the reason', () => {
    const r = scanPaste("PROCESS-NAME,it's\nDOMAIN-REGEX,a(b\nDOMAIN,{{x}}\nDOMAIN,fine.example\n")
    expect(r.entries.map(e => e.value)).toEqual(['fine.example'])
    expect(r.rejected.map(x => [x.line, x.reason])).toEqual([[1, 'quote'], [2, 'parens'], [3, 'template']])
  })

  it('the number a paste says it adds is the number of rule lines written, with the chosen target', () => {
    const d = D('- DOMAIN,a,X\n')
    const scan = scanPaste('PROCESS-NAME,it\'s\nb.example # c\nc.example\nDOMAIN,a,Z\n')
    const review = reviewPaste(scan.entries, d, buildBlocks(d), { kind: 'into', block: 0 })
    const after = insertRules(d, 1, review.add.map(e => ({ type: e.type, value: e.value, options: e.options })), 'X')
    expect(after.lines.filter(l => l.kind === 'rule' && l.rule!.target === 'X').length - 1).toBe(review.add.length)
    expect(review.add.map(e => e.value)).toEqual(['b.example', 'c.example'])
  })

  it('the row form refuses the same values', () => {
    expect(validateRuleValue('PROCESS-NAME', 'Foo # ads')).toBe('unsafe_comment')
    expect(validateRuleValue('DOMAIN-SUFFIX', 'example.com # ads')).not.toBeNull()
    expect(validateRuleValue('PROCESS-NAME', "it's")).toBe('unsafe_quote')
    expect(validateRuleValue('DOMAIN-SUFFIX', 'example.com')).toBeNull()
  })

  it('a new group name has to be writable too', () => {
    const c = { groups: ['Proxy'], rematch: [] }
    expect(newGroupNameProblem('Fast # x', c)).toBe('name_invalid')
    expect(newGroupNameProblem("it's", c)).toBe('name_invalid')
    expect(newGroupNameProblem('Proxy', c)).toBe('name_taken')
    expect(newGroupNameProblem('Fresh', c)).toBeNull()
  })
})

describe('editing writes only checked fields', () => {
  it('refuses to write an unsafe field', () => {
    expect(() => rewriteRule('- DOMAIN,a,X', { value: 'b # c' })).toThrow()
    expect(() => rewriteRule('- DOMAIN,a,X', { target: '' })).toThrow()
    expect(() => rewriteRule('- DOMAIN,a,X', { type: "it's" })).toThrow()
    expect(() => editRule(D('- DOMAIN,a,X\n'), 0, { value: "it's" })).toThrow()
  })

  it('leaves an existing unsafe field alone: the seeded AND line stays editable and retargetable', () => {
    const raw = '- AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制'
    expect(rewriteRule(raw, { value: '((NETWORK,UDP),(DST-PORT,443))' })).toBe(raw)
    expect(rewriteRule(raw, { target: 'REJECT' })).toBe('- AND,((NETWORK,UDP),(DST-PORT,443)),REJECT')
    expect(rewriteRule(raw, { type: 'and' })).toBe('- and,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制')
  })
})

describe('smaller faults (review small items)', () => {
  it('retargets the field the backend reads as the target, not the last equal one', () => {
    expect(text(retargetLines(D('- IP-CIDR,1.2.3.0/24,no-resolve,no-resolve\n'), [0], 'Q'))).toBe('- IP-CIDR,1.2.3.0/24,Q,no-resolve\n')
  })

  it('writes CRLF into a CRLF document and leaves the old last line alone when the document has no final newline', () => {
    const crlf = D('- DOMAIN,a,X\r\n- DOMAIN,b,Y\r\n')
    expect(text(insertRules(crlf, 1, [{ type: 'DOMAIN', value: 'n' }], 'X'))).toBe('- DOMAIN,a,X\r\n- DOMAIN,n,X\r\n- DOMAIN,b,Y\r\n')
    expect(text(newBlock(crlf, buildBlocks(crlf), 2, 'Z', [{ type: 'DOMAIN', value: 'z' }], 'why'))).toBe('- DOMAIN,a,X\r\n- DOMAIN,b,Y\r\n# why\r\n- DOMAIN,z,Z\r\n')
    expect(text(replaceRange(crlf, 0, 1, '- DOMAIN,q,X'))).toBe('- DOMAIN,q,X\r\n- DOMAIN,b,Y\r\n')
    const open = D('- DOMAIN,a,X\r\n- DOMAIN,b,Y')
    const out = text(insertRules(open, 2, [{ type: 'DOMAIN', value: 'n' }], 'Y'))
    expect(out).toBe('- DOMAIN,a,X\r\n- DOMAIN,b,Y\n- DOMAIN,n,Y')
    expect(diffLines(['- DOMAIN,a,X\r', '- DOMAIN,b,Y'], out.split('\n')).filter(o => o.kind === 'del')).toEqual([])
  })
})
