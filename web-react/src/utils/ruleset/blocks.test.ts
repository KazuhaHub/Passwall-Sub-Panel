import { describe, expect, it } from 'vitest'
import { blockAt, buildBlocks, noteOf } from './blocks'
import { defaultRulesContent } from './defaultRules.fixture'
import { parseDoc } from './parse'

const blocksOf = (s: string) => buildBlocks(parseDoc(s))

describe('groups', () => {
  it('splits the default rule set into the 29 groups the design counts', () => {
    const blocks = blocksOf(defaultRulesContent())
    expect(blocks).toHaveLength(29)
    expect(blocks.reduce((n, b) => n + b.count, 0)).toBe(9474)
    expect(blocks[0]).toMatchObject({ target: '🎯 全球直连', start: 0, end: 17, count: 17 })
    expect(blocks[1]).toMatchObject({ target: '⚡ QUIC控制', count: 1 })
    expect(blocks[blocks.length - 1]).toMatchObject({ target: '🐟 漏网之鱼', count: 1, fallback: true })
    expect(blocks.filter(b => b.target === '🎯 全球直连')).toHaveLength(3)
    expect(Math.max(...blocks.map(b => b.count))).toBe(6904)
  })

  it('does not overlap and covers every rule line exactly once', () => {
    const text = defaultRulesContent()
    const doc = parseDoc(text)
    const blocks = buildBlocks(doc)
    let prevEnd = 0
    for (const b of blocks) { expect(b.start).toBeGreaterThanOrEqual(prevEnd); prevEnd = b.end }
    const ruleLines = doc.lines.filter(l => l.kind === 'rule').length
    expect(blocks.reduce((n, b) => n + b.count, 0)).toBe(ruleLines)
  })

  it('ends a group at a comment, a blank line or a line it cannot read, and makes the comments above the next one its note', () => {
    const blocks = blocksOf('- DOMAIN,a,X\n- DOMAIN,b,X\n# why\n# this way\n- DOMAIN,c,X\n\n- DOMAIN,d,X\n- "q",\n- DOMAIN,e,X\n')
    expect(blocks.map(b => [b.kind, b.target, b.start, b.ruleStart, b.end])).toEqual([
      ['rules', 'X', 0, 0, 2],
      ['rules', 'X', 2, 4, 5],
      ['rules', 'X', 6, 6, 7],
      ['text', '', 7, 7, 8],
      ['rules', 'X', 8, 8, 9],
    ])
    expect(noteOf(parseDoc('# why\n# this way\n- DOMAIN,c,X\n'), buildBlocks(parseDoc('# why\n# this way\n- DOMAIN,c,X\n'))[0])).toBe('why\nthis way')
  })

  it('leaves a comment cut off by a blank line, and one at the end, outside every group', () => {
    const blocks = blocksOf('# orphan\n\n- DOMAIN,a,X\n# tail\n')
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ start: 2, end: 3 })
  })

  it('finds the block of a line, note included', () => {
    const blocks = blocksOf('- DOMAIN,a,X\n# n\n- DOMAIN,b,Y\n\n')
    expect(blockAt(blocks, 1)?.index).toBe(1)
    expect(blockAt(blocks, 2)?.index).toBe(1)
    expect(blockAt(blocks, 3)).toBeNull()
  })
})
