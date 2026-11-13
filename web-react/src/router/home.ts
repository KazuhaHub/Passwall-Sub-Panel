import type { Role } from '@/api/types'

export function homeForRole(role: Role | ''): string {
  // Operators land on the admin dashboard just like admins; the per-route
  // gating handles what they can actually do.
  return (role === 'admin' || role === 'operator') ? '/admin/dashboard' : '/user/me'
}

export function isAdminPath(path: string): boolean {
  return path === '/admin' || path.startsWith('/admin/')
}

// Admin-only routes that operators must not see in the sidebar and
// shouldn't be able to navigate to directly. Mirrors the backend's
// adminGroup vs staffGroup split — keep them in sync.
//
// The risk center is the third of the three gates that must name it: the
// `risk.view` capability (utils/permissions.ts) and the nav item's adminOnly
// flag (AdminLayout) are the other two. Without it here an operator who typed
// the URL would land on a page whose every read is a 403.
//
// The diagnostics page is the second page held by all three gates the same
// way: `diagnostics.view`, its nav item's adminOnly flag, and this entry.
// Every read and the clear behind it are adminGroup endpoints.
//
// Access control is the fourth page held by all three gates:
// `access.view`, its nav item's adminOnly flag, and this entry. Its lists,
// policies and exemptions are adminGroup, and so are the audit rows of a test.
const ADMIN_ONLY_ROUTES = [
  '/admin/servers',
  '/admin/settings',
  '/admin/risk',
  '/admin/diagnostics',
  '/admin/access-control',
]

export function isAdminOnlyPath(path: string): boolean {
  return ADMIN_ONLY_ROUTES.some(r => path === r || path.startsWith(r + '/'))
}
