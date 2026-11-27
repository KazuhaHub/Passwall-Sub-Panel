import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import * as icons from './icons'

const src = fileURLToPath(new URL('..', import.meta.url))
const doc = readFileSync(fileURLToPath(new URL('../../docs/icon-mapping.md', import.meta.url)), 'utf8')

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(tsx?|jsx?)$/.test(name) && !name.endsWith('.test.ts') && !name.endsWith('.test.tsx')) out.push(p)
  }
  return out
}

describe('@/lib/icons', () => {
  it('exports only Icon* names, each a real lucide component', () => {
    const names = Object.keys(icons)
    expect(names.length).toBeGreaterThan(100)
    for (const name of names) {
      expect(name).toMatch(/^Icon[A-Z]/)
      // A lucide icon is a forwardRef object; a missing re-export would be undefined.
      expect((icons as Record<string, unknown>)[name], name).toBeTruthy()
    }
  })

  it('has a table row for every semantic export, and an export for every table row', () => {
    const inTable = new Set([...doc.matchAll(/\| `(Icon[A-Za-z]+)` \|/g)].map(m => m[1]))
    const exported = new Set(Object.keys(icons))
    expect([...inTable].filter(n => !exported.has(n))).toEqual([])
    expect([...exported].filter(n => !inTable.has(n))).toEqual([])
  })

  // The module's own rule, enforced: only components/ui (the vendored primitives) and
  // this module may import lucide directly.
  it('is the only way the shared building blocks reach lucide', () => {
    const offenders = ['components/common', 'components/feedback', 'components/shell']
      .flatMap(d => walk(join(src, d)))
      .concat(join(src, 'components/ConfirmHost.tsx'))
      .filter(f => /from ['"]lucide-react['"]/.test(readFileSync(f, 'utf8')))
    expect(offenders).toEqual([])
  })
})
