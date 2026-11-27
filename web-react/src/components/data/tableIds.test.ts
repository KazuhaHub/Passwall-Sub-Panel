import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// Column widths are saved per table id, so every table somebody can reach needs one, and no two may share one
// (they would overwrite each other's widths). Reads the sources, so a new list page cannot forget it.
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap(n => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return sources(p)
    return /\.tsx$/.test(n) && !/\.test\./.test(n) ? [p] : []
  })
}

/** The opening tag starting at `start` (after `<DataTable`/`<LogList` and an optional `<Type>`), up to its closing `>`. */
function openingTag(text: string, start: number): string {
  let i = start + text.slice(start).match(/^<(?:DataTable|LogList)(?:<[A-Za-z]+>)?/)![0].length
  let depth = 0
  for (; i < text.length; i++) {
    const c = text[i]
    if (c === '{') depth++
    else if (c === '}') depth--
    else if (c === '>' && depth === 0 && text[i - 1] !== '=') break
  }
  return text.slice(start, i)
}

describe('DataTable call sites', () => {
  const found: { file: string; id: string | null }[] = []
  for (const file of sources(join(__dirname, '../../views'))) {
    const text = readFileSync(file, 'utf8')
    for (const m of text.matchAll(/<(?:DataTable|LogList)(?=[<\s])/g)) {
      const id = openingTag(text, m.index!).match(/tableId=(?:"([^"]+)"|\{([^}]+)\})/)
      found.push({ file: file.slice(file.indexOf('views')), id: id ? (id[1] ?? id[2]) : null })
    }
  }

  it('finds the call sites', () => { expect(found.length).toBeGreaterThan(25) })
  it('gives every one a tableId', () => { expect(found.filter(f => f.id === null)).toEqual([]) })
  it('does not reuse a literal id for two different tables, except one table drawn in two modes', () => {
    const literal = found.filter(f => f.id !== null && !f.id.startsWith('p.'))
    const byId = new Map<string, string[]>()
    for (const f of literal) byId.set(f.id!, [...(byId.get(f.id!) ?? []), f.file])
    const clashes = [...byId].filter(([, files]) => new Set(files).size !== files.length && !files.every(f => f.endsWith('BlockDirectory.tsx')))
    expect(clashes).toEqual([])
  })
})
