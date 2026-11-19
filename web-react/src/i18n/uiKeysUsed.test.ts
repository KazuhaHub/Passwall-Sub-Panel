import { describe, expect, it } from 'vitest'
import en from '../locales/en-US/common.json'
import zh from '../locales/zh-CN/common.json'

// The shared data, form and diagram blocks own common:ui.data / ui.form / ui.diagram.
// Every key under them must be read by some source file (a string nobody asks for is
// clutter the translators still carry), and every key a source file reads must exist
// in BOTH languages (a missing one shows as the raw key). Whole keys are matched, so
// `select_row` is not "used" because `select_row_named` is. Keys built at run time are
// listed here on purpose, with the values they can take; the test checks that list
// against the locale too.
const DYNAMIC: Record<string, string[]> = {
  'ui.diagram.kind_': ['user', 'entry', 'cdn', 'external', 'landing', 'exit', 'internet'],
  'ui.data.reorder_blocked_': ['sorted', 'tree', 'paged'],
  'ui.diagram.status_': ['pending', 'warning', 'danger'],
}
const GROUPS = ['data', 'form', 'diagram'] as const

const sources = import.meta.glob<string>(['../**/*.ts', '../**/*.tsx', '!../**/*.test.ts', '!../**/*.test.tsx'], { query: '?raw', import: 'default', eager: true })
const all = Object.values(sources).join('\n')

type Tree = Record<string, Record<string, string>>
const keysOf = (tree: Tree) => new Set(GROUPS.flatMap(g => Object.keys(tree[g] ?? {}).map(k => `ui.${g}.${k}`)))
// i18next plural forms: `x_one`, `x_other` are one key `x`.
const base = (key: string) => key.replace(/_(zero|one|two|few|many|other)$/, '')
const enKeys = keysOf((en as unknown as { ui: Tree }).ui)
const zhKeys = keysOf((zh as unknown as { ui: Tree }).ui)

const used = new Set([...all.matchAll(/['"`](?:common:)?(ui\.(?:data|form|diagram)\.[a-z_]+)['"`]/g)].map(m => m[1]))
const dynamicKeys = new Set(Object.entries(DYNAMIC).flatMap(([prefix, vs]) => vs.map(v => `${prefix}${v}`)))

describe('common:ui.data / ui.form / ui.diagram keys', () => {
  it('has no key that nothing reads', () => {
    const unused = [...enKeys].map(base).filter(k => !used.has(k) && !dynamicKeys.has(k))
    expect([...new Set(unused)]).toEqual([])
  })

  it('has every key that a source file reads, in both languages', () => {
    const present = (set: Set<string>) => new Set([...set].map(base))
    const missingEn = [...used].filter(k => !present(enKeys).has(k))
    const missingZh = [...used].filter(k => !present(zhKeys).has(k))
    expect(missingEn, 'read but missing from en-US').toEqual([])
    expect(missingZh, 'read but missing from zh-CN').toEqual([])
  })

  it('keeps the list of run-time keys honest: they all exist, and the source really builds them', () => {
    for (const [prefix, vs] of Object.entries(DYNAMIC)) {
      expect(new RegExp(`${prefix.replace(/\./g, '\\.')}(\\$\\{|' ?\\+)`).test(all) || all.includes(`${prefix}\${`), `${prefix}\${...} is not built anywhere`).toBe(true)
      for (const v of vs) {
        expect([...enKeys].map(base)).toContain(`${prefix}${v}`)
        expect([...zhKeys].map(base)).toContain(`${prefix}${v}`)
      }
    }
  })
})
