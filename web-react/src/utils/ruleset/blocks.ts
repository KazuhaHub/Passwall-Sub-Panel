import type { Doc } from './parse'

/**
 * THE GROUP VIEW (design 3.1): rows are the data, groups are only a way to look at them. A group is a run of rule
 * lines with the same target, uninterrupted by a comment, a blank line or a line the parser could not read; the
 * comment lines right above it are its note. Nothing here is stored: the same text always gives the same groups.
 *
 * Why not "all rules of one target": the same target appears in several places on purpose (a first match decides),
 * so merging them would change where traffic goes.
 */
export interface Block {
  kind: 'rules' | 'text'
  /** Position in the list of groups. */
  index: number
  /** The outbound every rule here names; '' for a text block or rules with no target. */
  target: string
  /** First line of the block, the note's first line when it has one. */
  start: number
  /** First rule line (equals `start` without a note). */
  ruleStart: number
  /** One past the last line. */
  end: number
  /** Rule lines (or text lines for a text block). */
  count: number
  /** Whether a MATCH rule is in it (the fallback group). */
  fallback: boolean
}

export function buildBlocks(doc: Doc): Block[] {
  const { lines } = doc
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (line.kind === 'rule') {
      const target = line.rule!.target
      let j = i + 1
      let fallback = line.rule!.type.toUpperCase() === 'MATCH'
      while (j < lines.length && lines[j].kind === 'rule' && lines[j].rule!.target === target) {
        if (lines[j].rule!.type.toUpperCase() === 'MATCH') fallback = true
        j++
      }
      // The note: comment lines directly above, with nothing between them and the first rule.
      let start = i
      while (start > 0 && lines[start - 1].kind === 'comment' && (blocks.length === 0 || start - 1 >= blocks[blocks.length - 1].end)) start--
      blocks.push({ kind: 'rules', index: blocks.length, target, start, ruleStart: i, end: j, count: j - i, fallback })
      i = j
    } else if (line.kind === 'text') {
      let j = i + 1
      while (j < lines.length && lines[j].kind === 'text') j++
      blocks.push({ kind: 'text', index: blocks.length, target: '', start: i, ruleStart: i, end: j, count: j - i, fallback: false })
      i = j
    } else i++
  }
  return blocks
}

/** The note of a block as plain text: its comment lines without the `#` and one following space. */
export function noteOf(doc: Doc, block: Block): string {
  const out: string[] = []
  for (let i = block.start; i < block.ruleStart; i++) out.push(doc.lines[i].raw.replace(/\r$/, '').trimStart().replace(/^#\s?/, ''))
  return out.join('\n')
}

/** The block a line belongs to (its note included), or null for a blank line or a comment that heads nothing. */
export function blockAt(blocks: readonly Block[], line: number): Block | null {
  let lo = 0
  let hi = blocks.length - 1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    const b = blocks[mid]
    if (line < b.start) hi = mid - 1
    else if (line >= b.end) lo = mid + 1
    else return b
  }
  return null
}
