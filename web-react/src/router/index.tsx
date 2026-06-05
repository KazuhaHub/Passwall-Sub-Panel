import { createBrowserRouter, Navigate, useRouteError, isRouteErrorResponse } from 'react-router-dom'
import { lazy } from 'react'
import { ErrorFallback } from '@/components/ErrorBoundary'
import RequireAuth from './RequireAuth'
import { homeForRole } from './home'
import { useAuthStore } from '@/stores/auth'

const LoginView = lazy(() => import('@/views/LoginView'))
const LoginLocalView = lazy(() => import('@/views/LoginLocalView'))
const SsoCallbackView = lazy(() => import('@/views/SsoCallbackView'))
const SsoNoAccountView = lazy(() => import('@/views/SsoNoAccountView'))
const SsoErrorView = lazy(() => import('@/views/SsoErrorView'))
const LoggedOutView = lazy(() => import('@/views/LoggedOutView'))
const ForgotPasswordView = lazy(() => import('@/views/ForgotPasswordView'))
const ResetPasswordView = lazy(() => import('@/views/ResetPasswordView'))
const AdminLayout = lazy(() => import('@/layouts/AdminLayout'))
const UserLayout = lazy(() => import('@/layouts/UserLayout'))
const PlaceholderView = lazy(() => import('@/views/admin/PlaceholderView'))
const DashboardView = lazy(() => import('@/views/admin/DashboardView'))
const ServersView = lazy(() => import('@/views/admin/ServersView'))
const CertificatesView = lazy(() => import('@/views/admin/CertificatesView'))
const GroupsView = lazy(() => import('@/views/admin/GroupsView'))
const UsersView = lazy(() => import('@/views/admin/UsersView'))
const NodesView = lazy(() => import('@/views/admin/NodesView'))
const RuleSetsView = lazy(() => import('@/views/admin/RuleSetsView'))
const TemplatesView = lazy(() => import('@/views/admin/TemplatesView'))
const LogsView = lazy(() => import('@/views/admin/LogsView'))
const SyncTasksView = lazy(() => import('@/views/admin/SyncTasksView'))
const TrafficView = lazy(() => import('@/views/admin/TrafficView'))
const SettingsView = lazy(() => import('@/views/admin/SettingsView'))
const MeView = lazy(() => import('@/views/user/MeView'))

// All admin pages migrated.
const PLACEHOLDER_PATHS: string[] = []

function RootRedirect() {
  const role = useAuthStore(s => s.role)
  return <Navigate to={homeForRole(role)} replace />
}

// RouteError renders the shared crash UI for errors react-router catches at
// the route level (a routed component's render throws, loader errors, 404
// Responses). Reuses ErrorFallback so a page-level crash looks the same as the
// app-level ErrorBoundary instead of react-router's built-in dev page.
function RouteError() {
  const err = useRouteError()
  const error = err instanceof Error
    ? err
    : new Error(isRouteErrorResponse(err) ? `${err.status} ${err.statusText}` : String(err))
  return <ErrorFallback error={error} onReload={() => window.location.reload()} />
}

export const router = createBrowserRouter([
  {
    // Root layout route: its errorElement catches render errors thrown by any
    // routed component. react-router intercepts those at the route level, so
    // without an errorElement here it would show its built-in dev error page.
    errorElement: <RouteError />,
    children: [
      {
        path: '/',
        element: <RootRedirect />,
      },
  { path: '/login', element: <LoginView /> },
  { path: '/login/local', element: <LoginLocalView /> },
  { path: '/sso-callback', element: <SsoCallbackView /> },
  { path: '/sso-no-account', element: <SsoNoAccountView /> },
  { path: '/sso-error', element: <SsoErrorView /> },
  { path: '/logged-out', element: <LoggedOutView /> },
  { path: '/forgot-password', element: <ForgotPasswordView /> },
  { path: '/reset-password', element: <ResetPasswordView /> },
  {
    path: '/admin',
    element: <RequireAuth adminOnly />,
    children: [
      {
        element: <AdminLayout />,
        children: [
          { index: true, element: <Navigate to="dashboard" replace /> },
          { path: 'dashboard', element: <DashboardView /> },
          { path: 'servers', element: <ServersView /> },
          { path: 'certs', element: <CertificatesView /> },
          { path: 'groups', element: <GroupsView /> },
          { path: 'users', element: <UsersView /> },
          { path: 'nodes', element: <NodesView /> },
          { path: 'rules', element: <RuleSetsView /> },
          { path: 'templates', element: <TemplatesView /> },
          { path: 'logs', element: <LogsView /> },
          { path: 'sync-tasks', element: <SyncTasksView /> },
          { path: 'traffic', element: <TrafficView /> },
          { path: 'settings', element: <SettingsView /> },
          ...PLACEHOLDER_PATHS.map(p => ({ path: p, element: <PlaceholderView /> })),
        ],
      },
    ],
  },
  {
    path: '/user',
    element: <RequireAuth />,
    children: [
      {
        element: <UserLayout />,
        children: [
          { index: true, element: <Navigate to="me" replace /> },
          { path: 'me', element: <MeView /> },
        ],
      },
    ],
  },
      // Catch-all: any unknown URL bounces to the role-based home.
      { path: '*', element: <RootRedirect /> },
    ],
  },
])
