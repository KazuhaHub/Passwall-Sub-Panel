import { describe, expect, it } from 'vitest'

import { ADMIN_NAV } from './adminNav'
import { isAdminOnlyPath } from '@/router/home'
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

// THE SHELL MUST NOT CHANGE WHO SEES WHAT. This is the admin navigation as the old
// layout drew it, pinned here by hand (the layout is gone, so there is nothing left to
// compare against): sections, entries, order, label keys and the adminOnly flags. A change
// to ADMIN_NAV has to change this list in the same commit, which is the review prompt.
describe('ADMIN_NAV is the console navigation', () => {
  const PINNED: { labelKey?: string; items: [to: string, labelKey: string, adminOnly?: true][] }[] = [
    { items: [['/admin/dashboard', 'nav:admin.dashboard']] },
    { labelKey: 'nav:section.directory', items: [['/admin/users', 'nav:admin.users'], ['/admin/groups', 'nav:admin.groups']] },
    { labelKey: 'nav:section.infrastructure', items: [
      ['/admin/servers', 'nav:admin.servers', true], ['/admin/nodes', 'nav:admin.nodes'], ['/admin/certs', 'nav:admin.certs', true],
    ] },
    { labelKey: 'nav:section.subscription', items: [
      ['/admin/rules', 'nav:admin.rules'], ['/admin/templates', 'nav:admin.templates'], ['/admin/sub-clients', 'nav:admin.sub_clients', true],
    ] },
    { labelKey: 'nav:section.reporting', items: [
      ['/admin/traffic', 'nav:admin.traffic'], ['/admin/logs', 'nav:admin.logs'], ['/admin/sync-tasks', 'nav:admin.sync_tasks'],
      ['/admin/node-issues', 'nav:admin.node_issues'], ['/admin/diagnostics', 'nav:admin.diagnostics', true],
    ] },
    { items: [['/admin/risk', 'nav:admin.risk_center', true], ['/admin/access-control', 'nav:admin.access_control', true]] },
    { items: [['/admin/language-packs', 'nav:admin.language_packs', true], ['/admin/settings', 'nav:admin.settings', true]] },
  ]

  it('has the pinned sections, entries, order and adminOnly flags', () => {
    const now = ADMIN_NAV.map(s => ({
      labelKey: s.labelKey,
      items: s.items.map(i => i.adminOnly ? [i.to, i.labelKey, true] : [i.to, i.labelKey]),
    }))
    expect(now).toEqual(PINNED)
  })

  it('shows an operator the ten staff entries and an admin all eighteen', () => {
    expect(visibleItems(ADMIN_NAV, 'operator').map(i => i.to)).toEqual([
      '/admin/dashboard', '/admin/users', '/admin/groups', '/admin/nodes', '/admin/rules', '/admin/templates',
      '/admin/traffic', '/admin/logs', '/admin/sync-tasks', '/admin/node-issues',
    ])
    expect(visibleItems(ADMIN_NAV, 'admin')).toHaveLength(18)
  })

  // The route guard (router/home.ts ADMIN_ONLY_ROUTES) is a known subset of the sidebar's adminOnly
  // flags (certs, sub-clients and language packs are hidden but not bounced). What must never
  // happen is the reverse: the sidebar offering an operator a page the guard bounces them from.
  it('never offers an operator a route the guard bounces them from', () => {
    for (const item of visibleItems(ADMIN_NAV, 'operator')) expect(isAdminOnlyPath(item.to), item.to).toBe(false)
  })
})
