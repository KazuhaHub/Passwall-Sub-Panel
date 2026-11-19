import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import { ADMIN_NAV } from './adminNav'
import { activeItem, isNavActive, visibleItems, visibleSections, type NavSection } from './nav'

const Icon = (() => null) as never
const nav: NavSection[] = [
  { items: [{ to: '/a', labelKey: 'k.a', icon: Icon }] },
  { labelKey: 's.one', items: [{ to: '/b', labelKey: 'k.b', icon: Icon, adminOnly: true }, { to: '/c', labelKey: 'k.c', icon: Icon }] },
  { labelKey: 's.two', items: [{ to: '/d', labelKey: 'k.d', icon: Icon, adminOnly: true }] },
]

describe('visibleSections', () => {
  it('shows an admin everything', () => {
    expect(visibleItems(nav, 'admin').map(i => i.to)).toEqual(['/a', '/b', '/c', '/d'])
  })

  it('hides adminOnly items from an operator and drops a section left empty, header and all', () => {
    const sections = visibleSections(nav, 'operator')
    expect(sections.map(s => s.labelKey)).toEqual([undefined, 's.one'])
    expect(sections[1].items.map(i => i.to)).toEqual(['/c'])
  })

  it('shows a signed-out or unknown role only what is not adminOnly', () => {
    expect(visibleItems(nav, '').map(i => i.to)).toEqual(['/a', '/c'])
  })

  it('does not mutate the model', () => {
    visibleSections(nav, 'operator')
    expect(nav[1].items).toHaveLength(2)
  })
})

describe('isNavActive', () => {
  it('matches the route and its children by prefix, not a sibling that shares a prefix', () => {
    expect(isNavActive('/admin/risk', '/admin/risk')).toBe(true)
    expect(isNavActive('/admin/risk/7', '/admin/risk')).toBe(true)
    expect(isNavActive('/admin/risks', '/admin/risk')).toBe(false)
    expect(isNavActive('/admin/users', '/admin/risk')).toBe(false)
  })

  it('picks the longest match when entries nest', () => {
    const nested: NavSection[] = [{ items: [{ to: '/x', labelKey: 'x', icon: Icon }, { to: '/x/y', labelKey: 'y', icon: Icon }] }]
    expect(activeItem(nested, 'admin', '/x/y/1')?.to).toBe('/x/y')
    expect(activeItem(nested, 'admin', '/x/other')?.to).toBe('/x')
    expect(activeItem(nested, 'admin', '/elsewhere')).toBeUndefined()
  })

  it('never reports an item the role cannot see', () => {
    expect(activeItem(nav, 'operator', '/d')).toBeUndefined()
  })
})

// THE SHELL MUST NOT CHANGE WHO SEES WHAT. ADMIN_NAV in layouts/AdminLayout.tsx is
// still the live model until the shell replaces it, so this reads that file and
// compares entry by entry: path, label key, adminOnly flag, section grouping and
// order. When AdminLayout.tsx is deleted this test goes with it.
describe('ADMIN_NAV parity with the layout it replaces', () => {
  const source = readFileSync(fileURLToPath(new URL('../../layouts/AdminLayout.tsx', import.meta.url)), 'utf8')
  const start = source.indexOf('const ADMIN_NAV')
  const block = source.slice(start, source.indexOf('export default function AdminLayout'))

  // Walk the old model's text: a section opens with `{ items: [` or
  // `{ labelKey: '...', items: [`, and each entry is one `{ to: ... }` object.
  const old: { labelKey?: string; items: { to: string; labelKey: string; adminOnly: boolean }[] }[] = []
  for (const line of block.split('\n')) {
    const section = /^\s*\{\s*(?:labelKey:\s*'([^']+)',\s*)?items:\s*\[\s*$/.exec(line)
    if (section) { old.push({ labelKey: section[1], items: [] }); continue }
    const item = /\{\s*to:\s*'([^']+)',\s*labelKey:\s*'([^']+)'[^}]*?(adminOnly:\s*true)?\s*\}/.exec(line)
    if (item) old[old.length - 1].items.push({ to: item[1], labelKey: item[2], adminOnly: Boolean(item[3]) })
  }

  it('found the old model (the reader is not silently empty)', () => {
    expect(old.length).toBe(7)
    expect(old.flatMap(s => s.items).length).toBe(18)
  })

  it('has the same sections, entries, order and adminOnly flags', () => {
    const now = ADMIN_NAV.map(s => ({
      labelKey: s.labelKey,
      items: s.items.map(i => ({ to: i.to, labelKey: i.labelKey, adminOnly: Boolean(i.adminOnly) })),
    }))
    expect(now).toEqual(old)
  })

  it('shows an operator exactly what the old filter showed', () => {
    const oldOperator = old.flatMap(s => s.items).filter(i => !i.adminOnly).map(i => i.to)
    expect(visibleItems(ADMIN_NAV, 'operator').map(i => i.to)).toEqual(oldOperator)
  })
})
