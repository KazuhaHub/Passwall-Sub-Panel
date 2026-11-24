/**
 * THE ONE QUESTION behind every write: can this text be a field of a rule line, `- TYPE,value,target`, and be read
 * back as exactly that field? A line is read twice by the backend: as a YAML plain scalar, then split at top-level
 * commas outside quotes and parentheses (ruleFieldSpans). Text that either reading would take differently is not
 * written, whatever the person typed or pasted; it is refused with the reason. Quoting would also survive both
 * readings, but it would make the line something the editor then treats as unreadable, so the model refuses instead.
 */
export type FieldProblem = 'empty' | 'comma' | 'newline' | 'space_edge' | 'comment' | 'colon' | 'quote' | 'template' | 'indicator' | 'parens'

const INDICATORS = '[{&*!|>%@`'

export function canWriteField(s: string): FieldProblem | null {
  if (s.trim() === '') return 'empty'
  if (/[\r\n]/.test(s)) return 'newline'
  if (s !== s.trim()) return 'space_edge'
  if (s.includes(',')) return 'comma'
  if (/(^| )#/.test(s)) return 'comment'
  if (s.includes(': ') || s.endsWith(':')) return 'colon'
  if (s.includes("'") || s.includes('"')) return 'quote'
  if (s.includes('{{')) return 'template'
  if (INDICATORS.includes(s[0]) || s.startsWith('- ') || s === '-') return 'indicator'
  let depth = 0
  for (const ch of s) {
    if (ch === '(') depth++
    else if (ch === ')') { if (depth === 0) return 'parens'; depth-- }
  }
  if (depth !== 0) return 'parens'
  return null
}
