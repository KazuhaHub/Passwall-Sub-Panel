import { useCallback } from 'react'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'

// Copy rule 8 (audit section 7.2): a space between Chinese and Latin letters or
// digits, none between two Chinese words, none next to full-width punctuation.
//
// Literal text in a template carries its own spaces ("按 Esc 取消"). The one thing a
// template cannot know is what an interpolated value will be: "{{name}}" is "HK 03"
// or "香港01", so whether a space belongs between the template's text and the value
// depends on the characters that actually meet. This resolves exactly those
// boundaries, after interpolation: a space only between a Chinese character on one
// side and a Latin letter or digit on the other. It never touches the inside of an
// interpolated value (a label stays what the cell shows), never adds a space next
// to punctuation, and never adds one where whitespace is already.
//
// How it sees the boundaries: for the call, each interpolated value is wrapped in
// two private-use sentinels (through i18next's per-call `interpolation.escape`), the
// finished string is walked once, and the sentinels are removed.
const OPEN = ''
const CLOSE = ''
const CJK = /[㐀-䶿一-鿿豈-﫿]/
const LATIN = /[A-Za-z0-9]/

const needsSpace = (a: string, b: string) => (CJK.test(a) && LATIN.test(b)) || (LATIN.test(a) && CJK.test(b))

/** Remove the sentinels, adding a space at each boundary between template text and a value (or two values) that needs one. */
export function resolveBoundaries(text: string): string {
  const pieces: { text: string; value: boolean }[] = []
  const re = new RegExp(`${OPEN}([^${CLOSE}]*)${CLOSE}`, 'g')
  let last = 0
  for (const m of text.matchAll(re)) {
    if (m.index > last) pieces.push({ text: text.slice(last, m.index), value: false })
    pieces.push({ text: m[1], value: true })
    last = m.index + m[0].length
  }
  if (last < text.length) pieces.push({ text: text.slice(last), value: false })

  let out = ''
  let lastWasValue = false
  for (const p of pieces) {
    if (p.text === '') continue
    // A number's sign or leading dot is part of the number: "例如 -9.5", not "例如-9.5".
    const lead = /^[-+]\.?\d|^\.\d/.test(p.text) ? '0' : p.text[0]
    if ((p.value || lastWasValue) && out !== '' && needsSpace(out[out.length - 1], lead)) out += ' '
    out += p.text
    lastWasValue = p.value
  }
  return out
}

/** `t` that resolves interpolation boundaries by the rule above. Use it for any key with a placeholder. */
export function spacedT(t: TFunction): TFunction {
  const spaced = (key: unknown, options?: Record<string, unknown>) => {
    const interpolation = {
      ...((options?.interpolation as object | undefined) ?? {}),
      escapeValue: true,
      escape: (value: string) => `${OPEN}${value}${CLOSE}`,
    }
    const result = (t as (k: unknown, o?: unknown) => unknown)(key, { ...options, interpolation })
    return typeof result === 'string' ? resolveBoundaries(result) : result
  }
  return spaced as unknown as TFunction
}

/** useTranslation('common') whose `t` resolves interpolation boundaries. */
export function useSpacedT() {
  const { t, i18n } = useTranslation('common')
  return { t: useCallback(spacedT(t), [t]), i18n } // eslint-disable-line react-hooks/exhaustive-deps
}
