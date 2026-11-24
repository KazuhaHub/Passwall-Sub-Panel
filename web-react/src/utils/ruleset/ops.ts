import type { Block } from './blocks'
import { parseDoc, parseLine, scanRule, targetIndex, type Doc, type Line, type Span } from './parse'

/**
 * Every edit the editor can make, as a pure function from document to document. The rule that matters (design 3.12,
 * invariant B): an edit changes only the lines it names. A line that is only moved keeps its text exactly; a line
 * that is changed keeps everything the change did not touch (spacing, the line ending); new lines are written in the
 * plain form `<indent>- TYPE,value,target[,option...]` with the indent and line ending of the lines beside them.
 */

export interface NewRule {
  type: string
  value: string
  options?: string[]
}

/** How new lines are written: the nearest rule line's indent and whether the document uses CRLF. */
export interface RuleStyle {
  indent: string
  cr: '' | '\r'
}

export function styleNear(doc: Doc, at: number): RuleStyle {
  const probe = (i: number): RuleStyle | null => {
    const line = doc.lines[i]
    if (!line) return null
    const scan = line.kind === 'rule' ? scanRule(line.raw) : null
    return { indent: scan?.indent ?? '', cr: line.raw.endsWith('\r') ? '\r' : '' }
  }
  for (let d = 0; d < doc.lines.length; d++) {
    for (const i of [at - 1 - d, at + d]) {
      const line = doc.lines[i]
      if (line?.kind === 'rule') return probe(i)!
    }
  }
  return { indent: '', cr: '' }
}

export function formatRule(rule: NewRule, target: string, style: RuleStyle): string {
  return `${style.indent}- ${rule.type},${rule.value},${target}${(rule.options ?? []).map(o => ',' + o).join('')}${style.cr}`
}

function splice(doc: Doc, start: number, remove: number, insert: Line[]): Doc {
  const lines = doc.lines.slice()
  lines.splice(start, remove, ...insert)
  // A document that had no lines ends with a newline once it has some, like every list a person saves.
  const trailingNewline = doc.lines.length === 0 && insert.length > 0 ? true : doc.trailingNewline
  return { lines, trailingNewline }
}

const trimmed = (raw: string, s: Span): Span => {
  let a = s.start
  let b = s.end
  while (a < b && /\s/.test(raw[a])) a++
  while (b > a && /\s/.test(raw[b - 1])) b--
  return { start: a, end: b }
}

export interface RulePatch {
  type?: string
  value?: string
  target?: string
  /** The whole list of options after the target. */
  options?: string[]
}

/**
 * Rewrites the named fields of one line in place. A field that already has the asked value is not touched, so a patch
 * that changes nothing returns the very same text. Not a rule line: returned as is.
 */
export function rewriteRule(raw: string, patch: RulePatch): string {
  const scan = scanRule(raw)
  if (!scan) return raw
  const { fields, spans } = scan
  let out = raw
  // Offsets stay valid if the edits run from the right end to the left.
  if (patch.options !== undefined && fields.length >= 3) {
    const current = fields.slice(3).filter(f => f !== '')
    if (current.join('\u0000') !== patch.options.join('\u0000')) {
      const end = trimmed(raw, spans[2]).end
      out = raw.slice(0, end) + patch.options.map(o => ',' + o).join('') + raw.slice(scan.bodyEnd)
    }
  }
  const replace = (index: number, value: string | undefined) => {
    if (value === undefined || index >= fields.length || fields[index] === value) return
    const s = trimmed(raw, spans[index])
    out = out.slice(0, s.start) + value + out.slice(s.end)
  }
  // The target is whichever field the backend reads it from (targetIndex), not always the third.
  replace(targetIndex(fields), patch.target)
  replace(1, patch.value)
  replace(0, patch.type)
  return out
}

/** Rewrites the target of a rule line wherever the backend finds it (the last real field; MATCH's second). */
function retargetRaw(raw: string, target: string): string {
  const line = parseLine(raw)
  const scan = scanRule(raw)
  if (!scan || !line.rule || line.rule.target === '' || line.rule.target === target) return raw
  const index = scan.fields.lastIndexOf(line.rule.target)
  const s = trimmed(raw, scan.spans[index])
  return raw.slice(0, s.start) + target + raw.slice(s.end)
}

function replaceLine(doc: Doc, i: number, raw: string): Doc {
  if (doc.lines[i].raw === raw) return doc
  const lines = doc.lines.slice()
  lines[i] = parseLine(raw)
  return { ...doc, lines }
}

export function editRule(doc: Doc, line: number, patch: RulePatch): Doc {
  return replaceLine(doc, line, rewriteRule(doc.lines[line].raw, patch))
}

export function retargetLines(doc: Doc, lines: readonly number[], target: string): Doc {
  const out = doc.lines.slice()
  let changed = false
  for (const i of lines) {
    const next = retargetRaw(out[i].raw, target)
    if (next !== out[i].raw) { out[i] = parseLine(next); changed = true }
  }
  return changed ? { ...doc, lines: out } : doc
}

export function renameTarget(doc: Doc, from: string, to: string): { doc: Doc; changed: number } {
  const hits = doc.lines.flatMap((l, i) => (l.kind === 'rule' && l.rule!.target === from ? [i] : []))
  return { doc: retargetLines(doc, hits, to), changed: hits.length }
}

export function insertRules(doc: Doc, at: number, rules: readonly NewRule[], target: string): Doc {
  if (rules.length === 0) return doc
  const style = styleNear(doc, at)
  return splice(doc, at, 0, rules.map(r => parseLine(formatRule(r, target, style))))
}

export function deleteLines(doc: Doc, lines: readonly number[]): Doc {
  if (lines.length === 0) return doc
  const drop = new Set(lines)
  return { ...doc, lines: doc.lines.filter((_, i) => !drop.has(i)) }
}

/** Removes `lines` and inserts the given lines at `at` (an index in the document BEFORE the removal). */
function relocate(doc: Doc, lines: readonly number[], at: number, make: (moved: Line[]) => Line[]): Doc {
  const sorted = [...new Set(lines)].sort((a, b) => a - b)
  const moved = sorted.map(i => doc.lines[i])
  const drop = new Set(sorted)
  const before = sorted.filter(i => i < at).length
  const rest = doc.lines.filter((_, i) => !drop.has(i))
  const lines2 = rest.slice()
  lines2.splice(at - before, 0, ...make(moved))
  return { ...doc, lines: lines2 }
}

const span = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => from + i)

/** Where a block inserted "before group k" lands: before that group (its note first), or after the last one. */
function slot(doc: Doc, blocks: readonly Block[], before: number): number {
  if (before < blocks.length) return blocks[before].start
  return blocks.length > 0 ? blocks[blocks.length - 1].end : doc.lines.length
}

/** Moves group `from` (note and rules, texts unchanged) to just before group `before`; `before === blocks.length` is the end. */
export function moveBlock(doc: Doc, blocks: readonly Block[], from: number, before: number): Doc {
  if (before === from || before === from + 1) return doc
  const b = blocks[from]
  return relocate(doc, span(b.start, b.end), slot(doc, blocks, before), m => m)
}

/** Moves rule lines into group `to`, after its last rule, and points them at its target. */
export function moveLinesToBlock(doc: Doc, blocks: readonly Block[], lines: readonly number[], to: number): Doc {
  const dest = blocks[to]
  const moved = relocate(doc, lines, dest.end, m => m)
  // Re-aim the moved lines at the target: they now sit just before where `dest` ends, shifted by those removed above it.
  const above = lines.filter(i => i < dest.end).length
  const first = dest.end - above
  return retargetLines(moved, span(first, first + lines.length), dest.target)
}

/** Moves rule lines into a group of their own with `target`, placed before group `before` (`blocks.length` = the end). */
export function moveLinesToNewBlock(doc: Doc, blocks: readonly Block[], lines: readonly number[], target: string, before: number): Doc {
  const at = slot(doc, blocks, before)
  const moved = relocate(doc, lines, at, m => m)
  const above = lines.filter(i => i < at).length
  const first = at - above
  return retargetLines(moved, span(first, first + lines.length), target)
}

/** Writes `text` as comment lines, one per line, in the style of the document. */
function noteLines(text: string, style: RuleStyle): Line[] {
  if (text.trim() === '') return []
  return text.replace(/\r\n?/g, '\n').split('\n').map(l => parseLine(`${style.indent}# ${l}`.trimEnd() + style.cr))
}

export function setNote(doc: Doc, block: Block, text: string): Doc {
  const current = doc.lines.slice(block.start, block.ruleStart).map(l => l.raw.replace(/\r$/, '').trimStart().replace(/^#\s?/, '')).join('\n')
  if (current === text) return doc
  return splice(doc, block.start, block.ruleStart - block.start, noteLines(text, styleNear(doc, block.ruleStart)))
}

export function deleteBlock(doc: Doc, block: Block): Doc {
  return splice(doc, block.start, block.end - block.start, [])
}

/** A new group with `rules` and an optional note, placed before group `before` (`blocks.length` = the end). */
export function newBlock(doc: Doc, blocks: readonly Block[], before: number, target: string, rules: readonly NewRule[], note = ''): Doc {
  const at = slot(doc, blocks, before)
  const style = styleNear(doc, at)
  const lines = [...noteLines(note, style), ...rules.map(r => parseLine(formatRule(r, target, style)))]
  return splice(doc, at, 0, lines)
}

/** What merging group `i` with the next would remove: only comments and blank lines may lie between them. */
export function mergePreview(doc: Doc, blocks: readonly Block[], i: number): { ok: boolean; lines: string[] } {
  const a = blocks[i]
  const b = blocks[i + 1]
  if (!a || !b || a.kind !== 'rules' || b.kind !== 'rules' || a.target !== b.target) return { ok: false, lines: [] }
  const between = doc.lines.slice(a.end, b.ruleStart)
  if (between.some(l => l.kind !== 'comment' && l.kind !== 'blank')) return { ok: false, lines: [] }
  return { ok: true, lines: between.map(l => l.raw.replace(/\r$/, '')).filter(l => l.trim() !== '') }
}

export function mergeBlocks(doc: Doc, blocks: readonly Block[], i: number): Doc {
  if (!mergePreview(doc, blocks, i).ok) return doc
  return splice(doc, blocks[i].end, blocks[i + 1].ruleStart - blocks[i].end, [])
}

/** Replaces the lines [start, end) with `text`, read again (the "edit as text" of a few lines). */
export function replaceRange(doc: Doc, start: number, end: number, text: string): Doc {
  const fresh = text === '' ? [] : parseDoc(text.replace(/\n$/, '')).lines
  return splice(doc, start, end - start, fresh)
}
