import { describe, expect, it } from 'vitest'
import zh from '../locales/zh-CN/common.json'

// Copy rule 8 (audit section 7.2): a space between a Chinese character and a Latin
// letter or digit, and also between a Chinese character and an interpolated value,
// because the value may be either ("{{name}}" is "HK 03" or "香港 03"). The spaces
// are written into the templates themselves; nothing transforms a label at run time.
// Full-width punctuation never takes a space, and is not matched here.
const CJK = '\\u3400-\\u4dbf\\u4e00-\\u9fff\\uf900-\\ufaff'
const rules: [string, RegExp][] = [
  ['Chinese directly before a Latin letter or digit', new RegExp(`[${CJK}][A-Za-z0-9]`)],
  ['Latin letter or digit directly before Chinese', new RegExp(`[A-Za-z0-9][${CJK}]`)],
  ['Chinese directly before a {{placeholder}}', new RegExp(`[${CJK}]\\{\\{`)],
  ['a {{placeholder}} directly before Chinese', new RegExp(`\\}\\}[${CJK}]`)],
]
// Cases the rule excludes: none needed so far. A template that really must be
// tight (before "%" or "°" the rule already allows it, they are not letters) is
// listed here as `key`, with a reason.
const ALLOW = new Set<string>([])

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
  it.each(Object.entries(ui).filter(([key]) => /^ui\.(data|form|diagram)\./.test(key)))('%s keeps rule 8', (key, text) => {
    if (ALLOW.has(key)) return
    const broken = rules.filter(([, re]) => re.test(text)).map(([why]) => why)
    expect(broken, `${key}: ${text}`).toEqual([])
  })
})
