import { describe, expect, it } from 'vitest'
import en from '../locales/en-US/common.json'

// A string nobody asks for is clutter the translators still have to carry. The
// shared data, form and diagram blocks own common:ui.data / ui.form / ui.diagram;
// every key under them must be read by some source file (a key built as
// `reorder_blocked_${reason}` is matched by its prefix).
const sources = import.meta.glob<string>(['../**/*.ts', '../**/*.tsx', '!../**/*.test.ts', '!../**/*.test.tsx'], { query: '?raw', import: 'default', eager: true })
const all = Object.values(sources).join('\n')

describe('common:ui.data / ui.form / ui.diagram', () => {
  const ui = (en as unknown as { ui: Record<string, Record<string, string>> }).ui
  for (const group of ['data', 'form', 'diagram'] as const) {
    it(`uses every ui.${group} key`, () => {
      const unused = Object.keys(ui[group] ?? {}).filter(key => {
        const full = `ui.${group}.${key}`
        if (all.includes(full)) return false
        const prefix = full.replace(/_[a-z]+$/, '_')
        return !all.includes(`${prefix}\${`)
      })
      expect(unused, `unused keys under ui.${group}`).toEqual([])
    })
  }
})
