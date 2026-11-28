import { describe, it, expect } from 'vitest'

import { NAMESPACES, flatten, type Nested } from './options'

// THE TWO SHIPPED LANGUAGES MUST CARRY THE SAME KEYS. fallbackLng sends an English
// reader to zh-CN for any key en-US lacks, so a string added to one file only is not
// an error anywhere: it is Chinese on an English page, or the reverse, and nothing
// reports it. Parity held at the time this was written (2,328 admin keys) by
// discipline alone. zh-TW is generated from zh-CN at build time and is not compared.
//
// Plural forms are compared by their base key: i18next asks English for `_one` and
// `_other` and Chinese for `_other` only, so `record_count_one` existing in en-US
// alone is correct.
const bundles = import.meta.glob<{ default: Nested }>('../locales/*/*.json', { eager: true })

function keysOf(lang: string, ns: string): Set<string> {
  const bundle = bundles[`../locales/${lang}/${ns}.json`]
  expect(bundle, `${lang}/${ns}.json is missing`).toBeDefined()
  return new Set(Object.keys(flatten(bundle.default)).map(key => key.replace(/_(zero|one|two|few|many|other)$/, '')))
}

describe('en-US and zh-CN locale parity', () => {
  it.each([...NAMESPACES])('%s has the same keys in both languages', ns => {
    const en = keysOf('en-US', ns)
    const zh = keysOf('zh-CN', ns)
    expect([...en].filter(key => !zh.has(key)), `keys in en-US/${ns}.json that zh-CN lacks`).toEqual([])
    expect([...zh].filter(key => !en.has(key)), `keys in zh-CN/${ns}.json that en-US lacks`).toEqual([])
  })
})

const sources = import.meta.glob<string>('../**/*.{ts,tsx}', { eager: true, query: '?raw', import: 'default' })
const LITERAL_KEY = /\bt\(\s*'(common|appearance|language|auth|nav|admin|user):([\w.-]+)'/g

/** Every whole `t('ns:key')` literal in non-test source, as `ns:key`. */
function literalKeys(): { file: string; ns: string; key: string }[] {
  const out: { file: string; ns: string; key: string }[] = []
  for (const [file, code] of Object.entries(sources)) {
    if (file.includes('.test.') || file.includes('/test/')) continue
    for (const m of code.matchAll(LITERAL_KEY)) {
      if (!m[2].endsWith('.')) out.push({ file, ns: m[1], key: m[2] }) // a trailing dot is a prefix completed at run time
    }
  }
  return out
}

// ENGLISH PLURALS MUST BE COMPLETE. en-US no longer falls back to zh-CN (the built-in
// languages load without a fallback bundle), so a key that exists only as `x_other`
// renders as the raw key for a count of exactly one. The base-key comparison above
// cannot see that. A plural form is told apart from a name that merely ends in
// `_other` by two things: it interpolates {{count}}, and the source does not ask for
// it by its full name (a key requested as `x_other` is a plain key called that).
describe('en-US plural forms', () => {
  const named = new Set(literalKeys().map(k => `${k.ns}:${k.key}`))
  it.each([...NAMESPACES])('%s: every counted _other key has a _one form', ns => {
    const flat = flatten(bundles[`../locales/en-US/${ns}.json`].default)
    const missing = Object.keys(flat)
      .filter(key => key.endsWith('_other') && String(flat[key]).includes('{{count}}') && !named.has(`${ns}:${key}`))
      .map(key => key.slice(0, -'_other'.length))
      .filter(base => !(`${base}_one` in flat) && !(base in flat))
    expect(missing, `en-US/${ns}.json plural keys with no _one form`).toEqual([])
  })
})

// A LITERAL KEY IN THE SOURCE MUST EXIST. Moving strings between namespaces (as the
// traffic chart's were, from admin to common) leaves any reader that was not updated
// showing its key. Only whole literal keys are checked; keys built at run time are
// covered by uiKeysUsed.test.ts.
describe('literal keys used in the source exist', () => {
  it('every t(\'ns:key\') literal resolves in en-US', () => {
    const missing: string[] = []
    const flats = new Map<string, Record<string, unknown>>(NAMESPACES.map(ns => [ns, flatten(bundles[`../locales/en-US/${ns}.json`].default)]))
    for (const { file, ns, key } of literalKeys()) {
      const flat = flats.get(ns)!
      const plural = ['_one', '_other'].some(suffix => `${key}${suffix}` in flat)
      if (!(key in flat) && !plural) missing.push(`${file}: ${ns}:${key}`)
    }
    expect(missing).toEqual([])
  })
})
