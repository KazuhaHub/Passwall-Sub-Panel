import { goTrim, normalizeTarget } from './target'
import { KNOWN_TYPES, RULE_OPTIONS, SIMPLE_TYPES } from './types'

/**
 * THE LOSSLESS MODEL of a rule set's `content` (design 3.1 and 3.12).
 *
 * The stored value stays one text. The editor reads it as lines, each keeping its ORIGINAL text, and an edit
 * replaces only the lines it names, so `serializeDoc(parseDoc(s)) === s` for every string `s` and an untouched
 * line is never reformatted. A line the parser understands also carries what it understood (`rule`); a line it does
 * not is `text` and is shown and written back as it is.
 *
 * What counts as a rule line is deliberately narrower than "valid YAML": `<spaces>- <plain scalar>`. The backend
 * reads `content` as a YAML list, so a quoted entry, a multi-line scalar or a line with a trailing ` #` comment
 * means more than the plain reading, and the editor leaves those to text mode instead of guessing.
 */

export type LineKind = 'rule' | 'comment' | 'blank' | 'text'

export interface RuleInfo {
  /** The type as written (`domain-suffix` stays lower case; the backend matches it case-insensitively). */
  type: string
  /** The second field. For a logical rule that is the whole parenthesised expression. */
  value: string
  /** The outbound, found and read the way the backend does (see `targetIndex` and `normalizeTarget`: quotes removed); '' when there is none. */
  target: string
  /** The target field as written (trimmed, quotes and all); '' when there is none. */
  targetRaw: string
  /** `no-resolve` / `src` after the target, in written order. */
  options: string[]
  /** Every top-level field, trimmed. */
  fields: string[]
  known: boolean
  /** Exactly `TYPE,value,target[,option...]` of a type the form can edit. */
  structured: boolean
}

export interface Line {
  /**
   * Who this line is, for as long as the document lives: assigned when the line is first read, carried by every edit
   * that keeps the line (a rewrite, a move) and never reused. Selections and open dialogs name lines by id, because a
   * line's index changes with every edit above it; an id that is gone means the line is gone.
   */
  id: number
  /** The line as it was, including a trailing `\r` when the document uses CRLF. Never contains `\n`. */
  raw: string
  kind: LineKind
  rule?: RuleInfo
}

export interface Doc {
  lines: Line[]
  /** The indent of the list: that of the first item (see `listIndent`). null when there is no item. */
  indent: string | null
  /** Lines outside the dialect the structured editor understands (see `foreignOf`). Any one makes the document text-only. */
  foreign: ForeignLine[]
  /** The text ended with a newline (the last element of `split('\n')` was empty). */
  trailingNewline: boolean
}

export type ForeignReason = 'continuation' | 'indent' | 'tab' | 'key' | 'marker' | 'block_scalar' | 'empty_item' | 'linebreak'
export interface ForeignLine { line: number; id: number; reason: ForeignReason }

export interface Span { start: number; end: number }

/**
 * Splits at commas that are outside parentheses and quotes: the backend's ruleFieldSpans, kept in step on purpose
 * (internal/service/render/line_names.go). Offsets are into `text`.
 */
export function fieldSpans(text: string): Span[] {
  const spans: Span[] = []
  let start = 0
  let depth = 0
  let quote = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      if (ch === '\\' && i + 1 < text.length) i++
      else if (ch === quote) {
        if (text[i + 1] === quote) i++
        else quote = ''
      }
      continue
    }
    if (ch === "'" || ch === '"') quote = ch
    else if (ch === '(') depth++
    else if (ch === ')') { if (depth > 0) depth-- }
    else if (ch === ',' && depth === 0) { spans.push({ start, end: i }); start = i + 1 }
  }
  spans.push({ start, end: text.length })
  return spans
}

export function splitFields(text: string): string[] {
  return fieldSpans(text).map(s => goTrim(text.slice(s.start, s.end)))
}

/**
 * ruleOutboundTargetIndex (internal/service/render/mihomo_advanced.go): which field is the outbound, or -1. Like the backend it
 * reads each field through `normalizeTarget` first, so a quoted `'no-resolve'` is still an option and not the target.
 */
export function targetIndex(fields: readonly string[]): number {
  if (fields.length < 2 || fields[0].toUpperCase() === 'SUB-RULE') return -1
  const minimum = fields[0].toUpperCase() === 'MATCH' ? 1 : 2
  for (let i = fields.length - 1; i >= minimum; i--) {
    const c = normalizeTarget(fields[i])
    if (c === '' || (i > minimum && RULE_OPTIONS.has(c.toLowerCase()))) continue
    return i
  }
  return -1
}

const RULE_RE = /^( *)-( +)(\S(?:[^]*\S)?)\s*$/

/** Whether `s` reads as a plain YAML scalar, with no quoting, flow, anchor, comment or key syntax in it. */
function plainScalar(s: string): boolean {
  if ('\'"[{&*!|>%@`#?:,'.includes(s[0])) return false
  if (s.startsWith('- ')) return false
  if (s.includes('{{') || /[ \t]#/.test(s) || /:[ \t]/.test(s) || s.endsWith(':') || /[\u2028\u2029\u0085]/.test(s)) return false
  return true
}

export interface RuleScan {
  indent: string
  /** Offset in `raw` where the scalar starts and ends (after the dash and its spaces; before trailing whitespace). */
  bodyStart: number
  bodyEnd: number
  /** Field spans as offsets into `raw`. */
  spans: Span[]
  fields: string[]
}

/** Finds the rule in a line, or null when the line is not a plain `- <scalar>` entry. */
export function scanRule(raw: string): RuleScan | null {
  const m = RULE_RE.exec(raw)
  if (!m || !plainScalar(m[3])) return null
  const bodyStart = m[1].length + 1 + m[2].length
  const bodyEnd = bodyStart + m[3].length
  const spans = fieldSpans(m[3]).map(s => ({ start: s.start + bodyStart, end: s.end + bodyStart }))
  return { indent: m[1], bodyStart, bodyEnd, spans, fields: spans.map(s => goTrim(raw.slice(s.start, s.end))) }
}

/**
 * Rule types whose payload may hold commas and which take no options: Mihomo reads everything between the type and the LAST
 * field as the payload (rules/common/base.go ParseRulePayload: `item[1:len-1]` joined with commas, the target at the end).
 */
export const PAYLOAD_SPANS_COMMAS: ReadonlySet<string> = new Set(['NOT', 'OR', 'AND', 'SUB-RULE', 'DOMAIN-REGEX', 'PROCESS-NAME-REGEX', 'PROCESS-PATH-REGEX'])

function ruleOf(scan: RuleScan): RuleInfo {
  const { fields } = scan
  const type = fields[0]
  const upper = type.toUpperCase()
  const index = targetIndex(fields)
  const targetRaw = index >= 0 ? fields[index] : ''
  const target = normalizeTarget(targetRaw)
  const options = index >= 0 ? fields.slice(index + 1).filter(f => f !== '') : []
  const quoted = fields.some(f => f[0] === '"' || f[0] === "'")
  const structured = SIMPLE_TYPES.has(upper) && !quoted && fields.length >= 3 && index === 2 && fields[1] !== ''
    && fields.slice(3).every(f => RULE_OPTIONS.has(f.toLowerCase()))
  // `^c{1,3}laude\.ai$` is one payload although the splitter cuts it at its comma: the fields up to the target are joined back.
  const value = PAYLOAD_SPANS_COMMAS.has(upper) && index > 2 ? fields.slice(1, index).join(',') : (fields[1] ?? '')
  return { type, value, target, targetRaw, options, fields, known: KNOWN_TYPES.has(upper), structured }
}

let nextId = 1
const newId = () => nextId++

const ITEM_RE = /^( *)-(?: |$)/

/**
 * The indent of the list. The backend reads `content` as a YAML sequence (parseRuleSequence in
 * internal/service/render/mihomo_advanced.go, yaml.Unmarshal into []string): the first item fixes the indent, an item
 * indented more is not an item but a continuation of the scalar before it, and one indented less ends the list.
 */
export function listIndent(doc: Pick<Doc, 'lines'>): string | null {
  for (const l of doc.lines) {
    const m = ITEM_RE.exec(l.raw)
    if (m) return m[1]
  }
  return null
}

/**
 * The lines `derive` made, so that `makeDoc` can tell a line it may hand on from an object that only looks like one (a
 * copy with another text spread over it). Lines are never changed after they are made.
 */
const derived = new WeakSet<object>()

function derive(raw: string, id: number, indent: string | null): Line {
  const line = deriveLine(raw, id, indent)
  derived.add(line)
  return line
}

function deriveLine(raw: string, id: number, indent: string | null): Line {
  if (raw.trim() === '') return { id, raw, kind: 'blank' }
  if (raw.trimStart().startsWith('#')) return { id, raw, kind: 'comment' }
  const item = ITEM_RE.exec(raw)
  if (item && indent !== null && item[1] !== indent) return { id, raw, kind: 'text' }
  const scan = scanRule(raw)
  return scan ? { id, raw, kind: 'rule', rule: ruleOf(scan) } : { id, raw, kind: 'text' }
}

/**
 * `l` itself when reading it again under `indent` would give an equal line, else null. What a line is depends on its
 * text and, for a list item, on whether its indent is the list's:
 *  - blank and comment lines are decided before the indent is looked at;
 *  - an item at another indent is `text` whatever else it holds;
 *  - a line that was a `rule` scanned as one, and the scan reads only the text, so it is that rule again at any indent
 *    that does not rule it out.
 * A `text` line at an indent that does not rule it out is read again: it may have been text only because the list's
 * indent used to be another (the first item was removed), and that is rare enough not to track.
 */
function reusable(l: { raw: string; id?: number }, indent: string | null): Line | null {
  if (!derived.has(l)) return null
  const line = l as Line
  if (line.kind === 'blank' || line.kind === 'comment') return line
  const item = ITEM_RE.exec(line.raw)
  const offIndent = item !== null && indent !== null && item[1] !== indent
  if (offIndent) return line.kind === 'text' ? line : null
  return line.kind === 'rule' ? line : null
}

/** One line on its own (no list to compare the indent with). */
export function parseLine(raw: string, indent: string | null = null, id: number = newId()): Line {
  return derive(raw, id, indent)
}

/**
 * Builds a document from lines (keeping the ids of those that have one). The kind of a line depends on the whole list
 * (its indent), so every edit comes back through here.
 *
 * A line that comes from another document and would read the same here is handed on as the same object (`reusable`).
 * The undo history keeps whole documents: with every line read again each step of the seeded rule set held about 3 MB,
 * with the untouched lines shared it holds its array of lines and the few that changed.
 */
export function makeDoc(lines: readonly { raw: string; id?: number }[], trailingNewline: boolean): Doc {
  const indent = listIndent({ lines: lines as Line[] })
  const out = lines.map(l => reusable(l, indent) ?? derive(l.raw, l.id ?? newId(), indent))
  return { lines: out, indent, foreign: foreignOf(out), trailingNewline }
}

export function parseDoc(text: string): Doc {
  if (text === '') return { lines: [], indent: null, foreign: [], trailingNewline: false }
  const parts = text.split('\n')
  const trailingNewline = parts[parts.length - 1] === ''
  if (trailingNewline) parts.pop()
  return makeDoc(parts.map(raw => ({ raw })), trailingNewline)
}

export function serializeDoc(doc: Doc): string {
  const body = doc.lines.map(l => l.raw).join('\n')
  return doc.trailingNewline ? body + '\n' : body
}

/**
 * THE DIALECT the structured editor understands, defined strictly instead of by listing the YAML shapes that go wrong.
 * A document is plain when every line is one of: blank; a full-line comment (`#` after optional spaces); a list item
 * `<indent>- <text>` at the one list indent (the first item's), spaces only. Every other line is FOREIGN: a continuation
 * line, an item at another indent, a tab in the indent or after the dash, a `key:` line or any non-item first line, a
 * `---` / `...` marker, a block-scalar header (`- |`, `- >`), an empty item, a line holding a Unicode line break. The
 * backend reads such lines in ways the editor does not model (a continuation folds into the item before it and can
 * change its target), so an edit next to one could change what the server reads: a document with any foreign line is
 * edited as text only, and says which lines and why.
 */
export function foreignOf(lines: readonly { raw: string; id: number }[]): ForeignLine[] {
  const out: ForeignLine[] = []
  let indent: string | null = null
  lines.forEach((l, line) => {
    const raw = l.raw.replace(/\r$/, '')
    const bad = (reason: ForeignReason) => out.push({ line, id: l.id, reason })
    if (/[\u2028\u2029\u0085]/.test(raw)) return bad('linebreak')
    if (raw.trim() === '') return
    if (/^ *#/.test(raw)) return
    if (/^---(\s|$)|^\.\.\.(\s|$)/.test(raw)) return bad('marker')
    if (/^ *\t/.test(raw) || /^ *-\t/.test(raw)) return bad('tab')
    const m = /^( *)- +(\S.*)$/.exec(raw)
    if (!m) {
      if (/^ *-\s*$/.test(raw)) return bad('empty_item')
      return bad(indent === null ? 'key' : 'continuation')
    }
    if (indent === null) indent = m[1]
    else if (m[1] !== indent) return bad('indent')
    if (/^[|>][-+0-9]*(\s|$)/.test(m[2])) return bad('block_scalar')
  })
  return out
}
