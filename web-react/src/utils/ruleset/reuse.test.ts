import { describe, expect, it } from 'vitest'
import { buildBlocks } from './blocks'
import { defaultRulesContent } from './defaultRules.fixture'
import {
  deleteBlock, deleteLines, editRule, insertRules, moveBlock, moveLinesToBlock, moveLinesToNewBlock, newBlock, renameTarget, replaceRange,
  retargetLines, setNote,
} from './ops'
import { makeDoc, parseDoc, serializeDoc, type Doc } from './parse'

/**
 * An edit hands back the lines it did not change AS THE SAME OBJECTS. The undo history keeps whole documents, and a
 * document whose 9,500 lines are all read again costs megabytes per step; sharing the untouched ones leaves a step with
 * little more than its array of lines. What a shared line says must still be exactly what reading it again would say.
 */

const small = '- DOMAIN,a.example,X\n- DOMAIN,b.example,X\n# second\n\n- DOMAIN,c.example,Y\n- IP-CIDR,10.0.0.0/8,Y,no-resolve\n# third\n- DOMAIN-SUFFIX,d.example,Z\n'

/** The document as reading every line again gives it (no line object carried over), ids kept. */
const reread = (d: Doc) => makeDoc(d.lines.map(l => ({ raw: l.raw, id: l.id })), d.trailingNewline)
const shared = (a: Doc, b: Doc) => { const had = new Set(a.lines); return b.lines.filter(l => had.has(l)).length }

describe('an edit shares the lines it leaves alone', () => {
  it('keeps every other line object when one rule is edited', () => {
    const before = parseDoc(small)
    const after = editRule(before, 1, { value: 'changed.example' })
    expect(after.lines[1]).not.toBe(before.lines[1])
    expect(after.lines[1].id).toBe(before.lines[1].id)
    before.lines.forEach((l, i) => { if (i !== 1) expect(after.lines[i]).toBe(l) })
  })

  it('keeps the objects of lines that only move', () => {
    const before = parseDoc(small)
    const after = moveBlock(before, buildBlocks(before), 0, 2)
    expect(serializeDoc(after)).not.toBe(small)
    expect(shared(before, after)).toBe(before.lines.length)
  })

  it('shares all but the touched lines of the seeded rule set', () => {
    const before = parseDoc(defaultRulesContent())
    const at = before.lines.findIndex(l => l.rule?.structured)
    const after = editRule(before, at, { value: 'changed.example' })
    expect(shared(before, after)).toBe(before.lines.length - 1)
    const added = insertRules(after, at, [{ type: 'DOMAIN', value: 'new.example' }], 'DIRECT')
    expect(shared(after, added)).toBe(after.lines.length)
  })

  it('never shares a copy of a line whose text was changed by hand', () => {
    const before = parseDoc(small)
    const forged = before.lines.map((l, i) => (i === 0 ? { ...l, raw: '# now a comment' } : l))
    const after = makeDoc(forged, true)
    expect(after.lines[0].kind).toBe('comment')
    expect(after.lines[0].rule).toBeUndefined()
  })
})

describe('a shared line says what reading it again would say', () => {
  it('reads the lines again when the indent of the list changes', () => {
    // The first item fixes the indent: with it gone the two-space items are the list, and the others stop being rules.
    const before = parseDoc('- DOMAIN,a.example,X\n  - DOMAIN,b.example,X\n  - DOMAIN,c.example,Y\n- DOMAIN,d.example,Z\n# note\n\n')
    expect(before.lines.map(l => l.kind)).toEqual(['rule', 'text', 'text', 'rule', 'comment', 'blank'])
    const after = deleteLines(before, [0])
    expect(after.lines.map(l => l.kind)).toEqual(['rule', 'rule', 'text', 'comment', 'blank'])
    expect(after).toEqual(reread(after))
    // And back: a line that was a rule under one indent is text under the other.
    const again = makeDoc([{ raw: '- DOMAIN,a.example,X' }, ...after.lines], true)
    expect(again.lines.map(l => l.kind)).toEqual(['rule', 'text', 'text', 'rule', 'comment', 'blank'])
    expect(again).toEqual(reread(again))
  })

  it('agrees with a full reading after every kind of edit', () => {
    const docs: Doc[] = []
    const start = parseDoc(small)
    const b = (d: Doc) => buildBlocks(d)
    docs.push(editRule(start, 0, { target: 'Y', options: [] }))
    docs.push(retargetLines(start, [0, 4], 'Q'))
    docs.push(renameTarget(start, 'X', 'W').doc)
    docs.push(insertRules(start, 2, [{ type: 'DOMAIN', value: 'n.example' }], 'X'))
    docs.push(deleteLines(start, [0, 1]))
    docs.push(moveBlock(start, b(start), 2, 0))
    docs.push(moveLinesToBlock(start, b(start), [0], 1))
    docs.push(moveLinesToNewBlock(start, b(start), [4, 5], 'N', 0))
    docs.push(setNote(start, b(start)[1], 'another\nnote'))
    docs.push(deleteBlock(start, b(start)[0]))
    docs.push(newBlock(start, b(start), 1, 'N', [{ type: 'DOMAIN', value: 'n.example' }], 'new'))
    docs.push(replaceRange(start, 0, 2, '  - DOMAIN,r.example,X\nkey: value'))
    for (const d of docs) {
      expect(d).toEqual(reread(d))
      expect(d).toEqual({ ...parseDoc(serializeDoc(d)), lines: parseDoc(serializeDoc(d)).lines.map((l, i) => ({ ...l, id: d.lines[i].id })), foreign: d.foreign })
    }
  })
})
