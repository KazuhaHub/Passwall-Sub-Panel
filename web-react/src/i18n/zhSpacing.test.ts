import { describe, expect, it } from 'vitest'
import zh from '../locales/zh-CN/common.json'

// Copy rule 8 for the zh-CN templates of common:ui.data / ui.form / ui.diagram.
//
// Literal text carries the spaces rule 8 asks for ("按 Esc 取消"). A PLACEHOLDER
// boundary carries none: what meets there is decided when the string is rendered
// (src/i18n/spacing.ts), because the value may be Chinese or Latin. So a template:
//   - has no space next to a {{placeholder}};
//   - has a space between a Chinese character and literal Latin text or a digit;
//   - has no space next to full-width punctuation.
const CJK = '\\u3400-\\u4dbf\\u4e00-\\u9fff\\uf900-\\ufaff'
const PUNCT = '，。；：、！？（）“”‘’《》'
const PLACEHOLDER = ''
const rules: [string, RegExp][] = [
  ['a hand-written space before a {{placeholder}}', new RegExp(`\\s${PLACEHOLDER}`)],
  ['a hand-written space after a {{placeholder}}', new RegExp(`${PLACEHOLDER}\\s`)],
  ['Chinese directly before literal Latin text or a digit', new RegExp(`[${CJK}][A-Za-z0-9]`)],
  ['literal Latin text or a digit directly before Chinese', new RegExp(`[A-Za-z0-9][${CJK}]`)],
  ['a space before full-width punctuation', new RegExp(`\\s[${PUNCT}]`)],
  ['a space after full-width punctuation', new RegExp(`[${PUNCT}]\\s`)],
]

function flat(obj: unknown, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else if (v && typeof v === 'object') Object.assign(out, flat(v, key))
  }
  return out
}

describe('zh-CN templates of common:ui.data / ui.form / ui.diagram', () => {
  const ui = flat((zh as unknown as { ui: unknown }).ui, 'ui')
  it.each(Object.entries(ui))('%s keeps rule 8', (key, text) => {
    // A placeholder stands for a value of unknown script, and its name has Latin letters in it.
    const template = text.replace(/\{\{[^}]*\}\}/g, PLACEHOLDER)
    const broken = rules.filter(([, re]) => re.test(template)).map(([why]) => why)
    expect(broken, `${key}: ${text}`).toEqual([])
  })

  it('checks the rules themselves: a bad template is caught', () => {
    const bad = (s: string) => rules.some(([, re]) => re.test(s.replace(/\{\{[^}]*\}\}/g, PLACEHOLDER)))
    expect(bad('选择 {{name}}')).toBe(true)
    expect(bad('{{n}} 项')).toBe(true)
    expect(bad('按Esc取消')).toBe(true)
    expect(bad('（{{x}}） 再')).toBe(true)
    expect(bad('选择{{name}}')).toBe(false)
    expect(bad('按 Esc 取消')).toBe(false)
  })
})
