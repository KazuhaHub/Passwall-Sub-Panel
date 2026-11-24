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
  /** The outbound, found the way the backend finds it (see `targetIndex`); '' when there is none. */
  target: string
  /** `no-resolve` / `src` after the target, in written order. */
  options: string[]
  /** Every top-level field, trimmed. */
  fields: string[]
  known: boolean
  /** Exactly `TYPE,value,target[,option...]` of a type the form can edit. */
  structured: boolean
}

export interface Line {
  /** The line as it was, including a trailing `\r` when the document uses CRLF. Never contains `\n`. */
  raw: string
  kind: LineKind
  rule?: RuleInfo
}

export interface Doc {
  lines: Line[]
  /** The text ended with a newline (the last element of `split('\n')` was empty). */
  trailingNewline: boolean
}

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
  return fieldSpans(text).map(s => text.slice(s.start, s.end).trim())
}

/** ruleOutboundTargetIndex (internal/service/render/mihomo_advanced.go): which field is the outbound, or -1. */
export function targetIndex(fields: readonly string[]): number {
  if (fields.length < 2 || fields[0].toUpperCase() === 'SUB-RULE') return -1
  const minimum = fields[0].toUpperCase() === 'MATCH' ? 1 : 2
  for (let i = fields.length - 1; i >= minimum; i--) {
    const c = fields[i]
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
  if (s.includes('{{') || s.includes(' #') || s.includes(': ') || s.endsWith(':')) return false
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
  return { indent: m[1], bodyStart, bodyEnd, spans, fields: spans.map(s => raw.slice(s.start, s.end).trim()) }
}

function ruleOf(scan: RuleScan): RuleInfo {
  const { fields } = scan
  const type = fields[0]
  const upper = type.toUpperCase()
  const index = targetIndex(fields)
  const target = index >= 0 ? fields[index] : ''
  const options = index >= 0 ? fields.slice(index + 1).filter(f => f !== '') : []
  const quoted = fields.some(f => f[0] === '"' || f[0] === "'")
  const structured = SIMPLE_TYPES.has(upper) && !quoted && fields.length >= 3 && index === 2 && fields[1] !== ''
    && fields.slice(3).every(f => RULE_OPTIONS.has(f.toLowerCase()))
  return { type, value: fields[1] ?? '', target, options, fields, known: KNOWN_TYPES.has(upper), structured }
}

export function parseLine(raw: string): Line {
  if (raw.trim() === '') return { raw, kind: 'blank' }
  if (raw.trimStart().startsWith('#')) return { raw, kind: 'comment' }
  const scan = scanRule(raw)
  return scan ? { raw, kind: 'rule', rule: ruleOf(scan) } : { raw, kind: 'text' }
}

export function parseDoc(text: string): Doc {
  if (text === '') return { lines: [], trailingNewline: false }
  const parts = text.split('\n')
  const trailingNewline = parts[parts.length - 1] === ''
  if (trailingNewline) parts.pop()
  return { lines: parts.map(parseLine), trailingNewline }
}

export function serializeDoc(doc: Doc): string {
  const body = doc.lines.map(l => l.raw).join('\n')
  return doc.trailingNewline ? body + '\n' : body
}
