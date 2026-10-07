import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, it } from 'vitest'

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(path)
    return /\.(ts|tsx)$/.test(entry.name) && !entry.name.includes('.test.') ? [path] : []
  })
}

it('keeps access-control views and shared status primitives on theme tokens', () => {
  const files = sourceFiles(fileURLToPath(new URL('../views/admin/accessControl', import.meta.url)))
  files.push(fileURLToPath(new URL('../views/admin/ServerAccessDialog.tsx', import.meta.url)))
  for (const name of ['ToneBadge', 'KpiTile', 'StatusLine', 'FieldHint']) {
    files.push(fileURLToPath(new URL(`../components/${name}.tsx`, import.meta.url)))
  }
  expect(files.length).toBeGreaterThan(20)
  const violations = files.flatMap(path => {
    const source = readFileSync(path, 'utf8')
    return [...source.matchAll(/#[0-9a-fA-F]{3,8}\b|rgba?\(|<Chip\b[^>]*\bcolor\s*=/g)].map(match => `${path}: ${match[0]}`)
  })
  expect(violations).toEqual([])
})
