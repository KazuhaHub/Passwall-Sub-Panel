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
const ADMIN_ONLY_ROUTES = [
  '/admin/servers',
  '/admin/settings',
]

export function isAdminOnlyPath(path: string): boolean {
  return ADMIN_ONLY_ROUTES.some(r => path === r || path.startsWith(r + '/'))
}
