import type { IconType } from '@/lib/icons'
import type { Role } from '@/api/types'

// THE NAVIGATION MODEL THE SHELL AND THE COMMAND PALETTE SHARE. The shell takes it
// as data and never imports the router's route table, so what a role can see is
// decided in one place (the model's owner) and filtered here the same way for the
// sidebar and for ⌘K.

export interface NavItem {
  to: string
  /** i18n key with its namespace, e.g. `nav:admin.users`. */
  labelKey: string
  icon: IconType
  /**
   * When true, the item only renders for the admin role (operators do not see it).
   * Mirrors the backend's adminGroup vs staffGroup split. This is one of the three
   * frontend gates (the others are ROLE_CAPS in utils/permissions.ts and
   * ADMIN_ONLY_ROUTES in router/home.ts); keep all three in step.
   */
  adminOnly?: boolean
}

// A section with a `labelKey` renders a small subheader above its items; a section
// without one holds standalone entries (Dashboard, Settings) outside any titled
// group. Role filtering runs per item, and a section whose items are all filtered
// out renders nothing, its header included.
export interface NavSection {
  labelKey?: string
  items: NavItem[]
}

export function canSee(item: NavItem, role: Role | ''): boolean {
  return !item.adminOnly || role === 'admin'
}

/** The sections a role sees: items filtered, empty sections dropped. */
export function visibleSections(nav: readonly NavSection[], role: Role | ''): NavSection[] {
  return nav
    .map(section => ({ ...section, items: section.items.filter(item => canSee(item, role)) }))
    .filter(section => section.items.length > 0)
}

/** Every item a role sees, in order. */
export function visibleItems(nav: readonly NavSection[], role: Role | ''): NavItem[] {
  return visibleSections(nav, role).flatMap(section => section.items)
}

/** Active by route prefix: `/admin/risk` is active on `/admin/risk` and `/admin/risk/7`, not on `/admin/risks`. */
export function isNavActive(pathname: string, to: string): boolean {
  return pathname === to || pathname.startsWith(to + '/')
}

export function activeItem(nav: readonly NavSection[], role: Role | '', pathname: string): NavItem | undefined {
  // The longest matching route wins, so a nested entry beats its parent.
  return visibleItems(nav, role)
    .filter(item => isNavActive(pathname, item.to))
    .sort((a, b) => b.to.length - a.to.length)[0]
}
