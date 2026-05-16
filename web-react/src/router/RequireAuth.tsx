import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { selectIsAdmin, selectIsLoggedIn, selectIsStaff, useAuthStore } from '@/stores/auth'
import { isAdminOnlyPath } from '@/router/home'

interface Props {
  /** Set on the /admin/* parent route. Operators count as staff and pass
   *  this check; pure-user accounts are bounced to their portal. The
   *  finer-grained "admin-only" path filter lives below. */
  adminOnly?: boolean
}

export default function RequireAuth({ adminOnly }: Props) {
  const location = useLocation()
  const role = useAuthStore(s => s.role)

  if (!selectIsLoggedIn()) {
    return <Navigate to="/login" state={{ returnTo: location.pathname + location.search }} replace />
  }
  if (adminOnly) {
    if (!selectIsStaff({ role } as never)) {
      return <Navigate to="/user/me" replace />
    }
    // Inside the admin SPA, some routes (servers / settings) hold
    // integration credentials and must stay admin-only even though
    // operators can browse the rest. Bounce operators to the dashboard
    // rather than 403 — looks intentional, not broken.
    if (!selectIsAdmin({ role } as never) && isAdminOnlyPath(location.pathname)) {
      return <Navigate to="/admin/dashboard" replace />
    }
  }
  return <Outlet />
}
