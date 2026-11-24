import { describe, expect, it } from 'vitest'
import { buildBlocks, noteOf } from './blocks'
import { defaultRulesContent } from './defaultRules.fixture'
import { diffLines } from './diff'
import {
  deleteBlock, deleteLines, editRule, insertRules, mergeBlocks, mergePreview, moveBlock, moveLinesToBlock, moveLinesToNewBlock, newBlock,
  renameTarget, replaceRange, retargetLines, rewriteRule, setNote, styleNear,
} from './ops'
import { parseDoc, serializeDoc, type Doc } from './parse'

const text = (d: Doc) => serializeDoc(d)
const raws = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))
/** What changed between two documents, as the lines removed and added (invariant B: only the lines an edit names). */
function changes(before: string, after: string) {
  const ops = diffLines(raws(before), raws(after))
  return { removed: ops.filter(o => o.kind === 'del').map(o => o.text), added: ops.filter(o => o.kind === 'add').map(o => o.text) }
}
const D = (s: string) => parseDoc(s)

const small = '- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n'

describe('rewriteRule keeps what it is not asked to change', () => {
  it('returns the line itself when nothing differs', () => {
    const raw = '  -   DOMAIN-SUFFIX ,  a.example , X  ,  no-resolve   \r'
    expect(rewriteRule(raw, {})).toBe(raw)
    expect(rewriteRule(raw, { type: 'DOMAIN-SUFFIX', value: 'a.example', target: 'X', options: ['no-resolve'] })).toBe(raw)
  })

  it('changes one field in place, leaving spacing and the line ending alone', () => {
    const raw = '  -   DOMAIN-SUFFIX ,  a.example , X  ,  no-resolve   \r'
    expect(rewriteRule(raw, { value: 'b.example' })).toBe('  -   DOMAIN-SUFFIX ,  b.example , X  ,  no-resolve   \r')
    expect(rewriteRule(raw, { target: 'Y' })).toBe('  -   DOMAIN-SUFFIX ,  a.example , Y  ,  no-resolve   \r')
    expect(rewriteRule(raw, { type: 'DOMAIN' })).toBe('  -   DOMAIN ,  a.example , X  ,  no-resolve   \r')
  })

  it('adds and removes options after the target', () => {
    expect(rewriteRule('- IP-CIDR,1.2.3.0/24,X\r', { options: ['no-resolve'] })).toBe('- IP-CIDR,1.2.3.0/24,X,no-resolve\r')
    expect(rewriteRule('- IP-CIDR,1.2.3.0/24,X,no-resolve', { options: [] })).toBe('- IP-CIDR,1.2.3.0/24,X')
    expect(rewriteRule('- IP-CIDR,1.2.3.0/24,X,src,no-resolve', { options: ['no-resolve'] })).toBe('- IP-CIDR,1.2.3.0/24,X,no-resolve')
  })

  it('changes several fields at once', () => {
    expect(rewriteRule('- DOMAIN,a.example,X', { type: 'DOMAIN-SUFFIX', value: 'b.example', target: 'Y' })).toBe('- DOMAIN-SUFFIX,b.example,Y')
  })

  it('leaves a line that is not a rule alone', () => {
    expect(rewriteRule('# note', { target: 'Y' })).toBe('# note')
    expect(rewriteRule("- 'DOMAIN,a,X'", { target: 'Y' })).toBe("- 'DOMAIN,a,X'")
  })
})

describe('single edits touch only their lines (invariant B)', () => {
  it('edit one row of the default rule set: one line out, one in', () => {
    const before = defaultRulesContent()
    const doc = D(before)
    const line = doc.lines.findIndex(l => l.raw === '- DOMAIN-SUFFIX,lan,🎯 全球直连')
    expect(line).toBeGreaterThan(0)
    const after = text(editRule(doc, line, { value: 'lan2' }))
    expect(changes(before, after)).toEqual({ removed: ['- DOMAIN-SUFFIX,lan,🎯 全球直连'], added: ['- DOMAIN-SUFFIX,lan2,🎯 全球直连'] })
  })

  it('opening a row and saving it unchanged produces no difference', () => {
    const before = defaultRulesContent()
    const doc = D(before)
    expect(text(editRule(doc, 6, { type: doc.lines[6].rule!.type, value: doc.lines[6].rule!.value, options: doc.lines[6].rule!.options }))).toBe(before)
  })

  it('add rules at the end of a group', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    const after = text(insertRules(doc, blocks[0].end, [{ type: 'DOMAIN-SUFFIX', value: 'new.example' }], 'X'))
    expect(after).toBe('- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n- DOMAIN-SUFFIX,new.example,X\n# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
  })

  it('new lines take the indent and line ending of their neighbours', () => {
    const doc = D('  - DOMAIN,a.example,X\r\n  - DOMAIN,b.example,X\r\n')
    expect(styleNear(doc, 2)).toEqual({ indent: '  ', cr: '\r' })
    expect(text(insertRules(doc, 2, [{ type: 'DOMAIN', value: 'c.example', options: [] }], 'X'))).toBe('  - DOMAIN,a.example,X\r\n  - DOMAIN,b.example,X\r\n  - DOMAIN,c.example,X\r\n')
  })

  it('writes into an empty document with a final newline, no indent', () => {
    expect(text(insertRules(D(''), 0, [{ type: 'DOMAIN', value: 'a.example' }], 'X'))).toBe('- DOMAIN,a.example,X\n')
  })

  it('writes no-resolve on an IP rule when asked', () => {
    expect(text(insertRules(D(''), 0, [{ type: 'IP-CIDR', value: '1.2.3.0/24', options: ['no-resolve'] }], 'X'))).toBe('- IP-CIDR,1.2.3.0/24,X,no-resolve\n')
  })

  it('delete rows', () => {
    const doc = D(small)
    expect(changes(small, text(deleteLines(doc, [1, 4])))).toEqual({ removed: ['- DOMAIN,b.example,X', '- IP-CIDR,10.0.0.0/8,Y,no-resolve'], added: [] })
  })

  it('changing a group target rewrites its rules and nothing else', () => {
    const before = defaultRulesContent()
    const doc = D(before)
    const blocks = buildBlocks(doc)
    const b = blocks[1] // the QUIC group, one AND rule
    const after = text(retargetLines(doc, range(b.ruleStart, b.end), 'REJECT'))
    expect(changes(before, after)).toEqual({ removed: ['- AND,((NETWORK,UDP),(DST-PORT,443)),⚡ QUIC控制'], added: ['- AND,((NETWORK,UDP),(DST-PORT,443)),REJECT'] })
  })

  it('changing the target of the 6,904-rule group changes exactly those lines', () => {
    const before = defaultRulesContent()
    const doc = D(before)
    const blocks = buildBlocks(doc)
    const big = blocks.find(b => b.count === 6904)!
    const after = text(retargetLines(doc, range(big.ruleStart, big.end), '🚀 新目标'))
    const c = changes(before, after)
    expect(c.removed).toHaveLength(6904)
    expect(c.added).toHaveLength(6904)
    expect(c.added.every(l => l.endsWith(',🚀 新目标') || l.endsWith(',🚀 新目标,no-resolve'))).toBe(true)
  })

  it('renames a target everywhere it is used', () => {
    const doc = D(small)
    const r = renameTarget(doc, 'Y', 'Q')
    expect(r.changed).toBe(2)
    expect(text(r.doc)).toBe('- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# second\n- DOMAIN,c.example,Q\n- IP-CIDR,10.0.0.0/8,Q,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
  })
})

describe('moving', () => {
  it('moves a group with its note and changes no line text', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    const moved = moveBlock(doc, blocks, 1, 0)
    expect(text(moved)).toBe('# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
    expect([...raws(text(moved))].sort()).toEqual([...raws(small)].sort())
  })

  it('moves a group to the end', () => {
    const doc = D(small)
    expect(text(moveBlock(doc, buildBlocks(doc), 0, 3))).toBe('# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n')
  })

  it('a move that lands where it was changes nothing', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    expect(moveBlock(doc, blocks, 1, 1)).toBe(doc)
    expect(moveBlock(doc, blocks, 1, 2)).toBe(doc)
  })

  it('moving a group in the default rule set is a pure reordering of lines', () => {
    const before = defaultRulesContent()
    const doc = D(before)
    const blocks = buildBlocks(doc)
    const after = text(moveBlock(doc, blocks, 5, 2))
    expect(after.length).toBe(before.length)
    expect(raws(after).sort()).toEqual(raws(before).sort())
    const nb = buildBlocks(D(after))
    expect(nb).toHaveLength(29)
    expect(nb[2].target).toBe(blocks[5].target)
  })

  it('moves rows into another group and gives them its target', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    const after = text(moveLinesToBlock(doc, blocks, [0], 1))
    expect(after).toBe('- DOMAIN,b.example,X\n# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n- DOMAIN,a.example,Y\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
  })

  it('moves rows into a new group before or after another', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    expect(text(moveLinesToNewBlock(doc, blocks, [5 + 1], 'N', 0))).toBe('- DOMAIN-SUFFIX,d.example,N\n- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n')
  })
})

describe('groups', () => {
  it('adds a group with a note at a position', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    const after = text(newBlock(doc, blocks, 1, 'N', [{ type: 'DOMAIN-SUFFIX', value: 'n.example' }], 'why\nbecause'))
    expect(after).toBe('- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# why\n# because\n- DOMAIN-SUFFIX,n.example,N\n# second\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
  })

  it('deletes a group with its note', () => {
    const doc = D(small)
    expect(text(deleteBlock(doc, buildBlocks(doc)[1]))).toBe('- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# third\n- DOMAIN-SUFFIX,d.example,Z\n')
  })

  it('replaces, adds and clears a note', () => {
    const doc = D(small)
    const blocks = buildBlocks(doc)
    expect(noteOf(doc, blocks[1])).toBe('second')
    expect(text(setNote(doc, blocks[1], 'edited\nmore'))).toBe(small.replace('# second\n', '# edited\n# more\n'))
    expect(text(setNote(doc, blocks[0], 'first'))).toBe('# first\n' + small)
    expect(text(setNote(doc, blocks[1], ''))).toBe(small.replace('# second\n', ''))
    expect(setNote(doc, blocks[1], 'second')).toBe(doc)
  })

  it('merges two neighbours of the same target by removing what is between them', () => {
    const doc = D('- DOMAIN,a,X\n# keep apart\n- DOMAIN,b,X\n- DOMAIN,c,Y\n')
    const blocks = buildBlocks(doc)
    expect(mergePreview(doc, blocks, 0)).toEqual({ ok: true, lines: ['# keep apart'] })
    expect(text(mergeBlocks(doc, blocks, 0))).toBe('- DOMAIN,a,X\n- DOMAIN,b,X\n- DOMAIN,c,Y\n')
    expect(mergePreview(doc, blocks, 1).ok).toBe(false)
  })

  it('edits some lines as text, parsing them again', () => {
    const doc = D(small)
    const after = replaceRange(doc, 3, 5, '- DOMAIN,c.example,Y\n- AND,((A,b),(C,d)),Y\n- SUB-RULE,(NETWORK,UDP),ai')
    expect(text(after)).toBe(small.replace('- IP-CIDR,10.0.0.0/8,Y,no-resolve\n', '- AND,((A,b),(C,d)),Y\n- SUB-RULE,(NETWORK,UDP),ai\n'))
    expect(after.lines[4].rule!.type).toBe('AND')
  })
})

function range(from: number, to: number) { return Array.from({ length: to - from }, (_, i) => from + i) }
