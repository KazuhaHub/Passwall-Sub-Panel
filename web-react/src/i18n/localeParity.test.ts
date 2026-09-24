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
