import { createBrowserRouter, Navigate, useRouteError, isRouteErrorResponse } from 'react-router'
import { lazy } from 'react'
import { ErrorFallback } from '@/components/ErrorBoundary'
import RequireAuth from './RequireAuth'
import NotFoundView from '@/views/NotFoundView'
import { homeForRole } from './home'
import { useAuthStore } from '@/stores/auth'
import { panelPath } from '@/panelPath'
import { viewLoaders } from './viewModules'
import LogsRoute from './LogsRoute'
import { loadAdminShell, loadEnroll2FA, loadUserShell, withAllNamespaces } from './shellLoaders'

const LoginView = lazy(() => import('@/views/LoginView'))
const SsoCallbackView = lazy(() => import('@/views/SsoCallbackView'))
const SsoNoAccountView = lazy(() => import('@/views/SsoNoAccountView'))
const SsoErrorView = lazy(() => import('@/views/SsoErrorView'))
const LoggedOutView = lazy(() => import('@/views/LoggedOutView'))
const ForgotPasswordView = lazy(() => import('@/views/ForgotPasswordView'))
const ResetPasswordView = lazy(() => import('@/views/ResetPasswordView'))
const RegisterView = lazy(() => import('@/views/RegisterView'))
const Enroll2FAView = lazy(loadEnroll2FA)
const VerifyEmailView = lazy(() => import('@/views/VerifyEmailView'))
const LegalView = lazy(() => import('@/views/LegalView'))
const AdminShellRoute = lazy(loadAdminShell)
const UserShellRoute = lazy(loadUserShell)
const PlaceholderView = lazy(() => import('@/views/admin/PlaceholderView'))
const DashboardView = lazy(viewLoaders['/admin/dashboard'])
const ServersView = lazy(viewLoaders['/admin/servers'])
const CertificatesView = lazy(viewLoaders['/admin/certs'])
const GroupsView = lazy(viewLoaders['/admin/groups'])
const UsersView = lazy(viewLoaders['/admin/users'])
const ServerCreateRoute = lazy(() => import('@/views/admin/servers/ServerCreateRoute'))
const ServerRoute = lazy(() => import('@/views/admin/servers/ServerRoute'))
const ServerInstallRoute = lazy(() => import('@/views/admin/servers/ServerInstallRoute'))
const GroupDetailRoute = lazy(() => import('@/views/admin/groups/GroupDetailRoute'))
const GroupCreatePage = lazy(() => import('@/views/admin/groups/GroupCreatePage'))
const UserDetailRoute = lazy(() => import('@/views/admin/users/UserDetailRoute'))
const UserCreatePage = lazy(() => import('@/views/admin/users/UserCreatePage'))
const LegalEditorRoute = lazy(() => import('@/views/admin/legal/LegalEditorRoute'))
const NodesPage = lazy(viewLoaders['/admin/nodes'])
const NodeDetailRoute = lazy(() => import('@/views/admin/nodes/NodeDetailRoute'))
const NodeCreateRoute = lazy(() => import('@/views/admin/nodes/NodeCreateRoute'))
const RuleSetsView = lazy(viewLoaders['/admin/rules'])
const RuleSetRoute = lazy(() => import('@/views/admin/rulesets/RuleSetRoute'))
const RuleSetCreateRoute = lazy(() => import('@/views/admin/rulesets/RuleSetRoute').then(m => ({ default: m.RuleSetCreateRoute })))
const TemplatesView = lazy(viewLoaders['/admin/templates'])
const TemplateRoute = lazy(() => import('@/views/admin/templates/TemplateRoute'))
const TemplateCreateRoute = lazy(() => import('@/views/admin/templates/TemplateRoute').then(m => ({ default: m.TemplateCreateRoute })))
const SubClientsView = lazy(viewLoaders['/admin/sub-clients'])
const LogsView = lazy(viewLoaders['/admin/logs'])
const RiskCenterView = lazy(viewLoaders['/admin/risk'])
const AccessControlView = lazy(viewLoaders['/admin/access-control'])
// A policy and a list are pages of their own under the access-control page; their create forms sit beside them.
const PolicyDetailRoute = lazy(() => import('@/views/admin/accessControl/policies/PolicyRoutes').then(m => ({ default: m.PolicyDetailRoute })))
const PolicyCreateRoute = lazy(() => import('@/views/admin/accessControl/policies/PolicyRoutes').then(m => ({ default: m.PolicyCreateRoute })))
const ListDetailRoute = lazy(() => import('@/views/admin/accessControl/lists/ListRoutes').then(m => ({ default: m.ListDetailRoute })))
const ListCreateRoute = lazy(() => import('@/views/admin/accessControl/lists/ListRoutes').then(m => ({ default: m.ListCreateRoute })))
const SyncTasksView = lazy(viewLoaders['/admin/sync-tasks'])
const NodeIssuesView = lazy(viewLoaders['/admin/node-issues'])
const DiagnosticsView = lazy(viewLoaders['/admin/diagnostics'])
const TrafficView = lazy(viewLoaders['/admin/traffic'])
const SettingsView = lazy(viewLoaders['/admin/settings'])
const LanguagePacksView = lazy(viewLoaders['/admin/language-packs'])
const MeView = lazy(viewLoaders['/user/me'])
// Dev-only component gallery for the Tailwind/shadcn stack. The condition is a
// compile-time constant, so a production build drops both the route and the
// lazy chunk.
const UiKitchenSink = import.meta.env.DEV ? lazy(withAllNamespaces(() => import('@/views/admin/UiKitchenSink'))) : null
const UiKitData = import.meta.env.DEV ? lazy(withAllNamespaces(() => import('@/views/admin/UiKitData'))) : null
const UiKitShell = import.meta.env.DEV ? lazy(withAllNamespaces(() => import('@/views/admin/UiKitShell'))) : null
const UiKitUserShell = import.meta.env.DEV ? lazy(withAllNamespaces(() => import('@/views/admin/UiKitShell').then(m => ({ default: m.UiKitUserShell })))) : null

// All admin pages migrated.
const PLACEHOLDER_PATHS: string[] = []

function AdminNotFound() { return <NotFoundView home="/admin/dashboard" /> }
function UserNotFound() { return <NotFoundView home="/user/me" /> }

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
  ...(UiKitchenSink ? [{ path: '/__ui-kit', element: <UiKitchenSink /> }] : []),
  ...(UiKitData ? [{ path: '/__ui-kit/data', element: <UiKitData /> }] : []),
  ...(UiKitShell ? [{ path: '/__ui-kit/shell/*', element: <UiKitShell /> }] : []),
  ...(UiKitUserShell ? [{ path: '/__ui-kit/shell-user', element: <UiKitUserShell /> }] : []),
  { path: '/login', element: <LoginView /> },
  { path: '/login/local', element: <LoginView forceLocal /> },
  { path: '/sso-callback', element: <SsoCallbackView /> },
  { path: '/sso-no-account', element: <SsoNoAccountView /> },
  { path: '/sso-error', element: <SsoErrorView /> },
  { path: '/logged-out', element: <LoggedOutView /> },
  { path: '/forgot-password', element: <ForgotPasswordView /> },
  { path: '/reset-password', element: <ResetPasswordView /> },
  { path: '/register', element: <RegisterView /> },
  { path: '/verify-email', element: <VerifyEmailView /> },
  // Terms and privacy: public, like the login page they are linked from.
  { path: '/legal/:kind', element: <LegalView /> },
  {
    path: '/admin',
    element: <RequireAuth adminOnly />,
    children: [
      {
        element: <AdminShellRoute />,
        children: [
          { index: true, element: <Navigate to="dashboard" replace /> },
          { path: 'dashboard', element: <DashboardView /> },
          { path: 'servers', element: <ServersView /> },
          // A server, its creation and a Passwall Node's installation are pages of their own (they were side sheets over the list).
          { path: 'servers/new', element: <ServerCreateRoute /> },
          { path: 'servers/:id', element: <ServerRoute /> },
          { path: 'servers/:id/install', element: <ServerInstallRoute /> },
          { path: 'certs', element: <CertificatesView /> },
          { path: 'groups', element: <GroupsView /> },
          // A group is a page of its own; `?edit=<id>` on the list (the old side sheet's address) is sent on to these.
          { path: 'groups/new', element: <GroupCreatePage /> },
          { path: 'groups/:id', element: <GroupDetailRoute /> },
          { path: 'users', element: <UsersView /> },
          // A user is a page of its own; `?user=<id>` on the list (the old side sheet's address) is sent on to these.
          { path: 'users/new', element: <UserCreatePage /> },
          { path: 'users/:id', element: <UserDetailRoute /> },
          { path: 'nodes', element: <NodesPage /> },
          // Admin only (D3); until N2 and N3 these shells offer the legacy editors.
          { path: 'nodes/new', element: <NodeCreateRoute /> },
          { path: 'nodes/:id', element: <NodeDetailRoute /> },
          { path: 'rules', element: <RuleSetsView /> },
          // A rule set is a page of its own; `?edit=` on the list (the old side sheet's address) is sent on to these.
          { path: 'rules/new', element: <RuleSetCreateRoute /> },
          { path: 'rules/:slug', element: <RuleSetRoute /> },
          { path: 'templates', element: <TemplatesView /> },
          // A template is a page of its own; `?edit=` on the list (the old side sheet's address) is sent on to these.
          { path: 'templates/new', element: <TemplateCreateRoute /> },
          { path: 'templates/:slug', element: <TemplateRoute /> },
          { path: 'sub-clients', element: <SubClientsView /> },
          // LogsRoute sends an admin's old ?tab=geo|risk links on to the risk
          // center, where those two tabs now live.
          { path: 'logs', element: <LogsRoute><LogsView /></LogsRoute> },
          { path: 'risk', element: <RiskCenterView /> },
          { path: 'access-control', element: <AccessControlView /> },
          { path: 'access-control/policies/new', element: <PolicyCreateRoute /> },
          { path: 'access-control/policies/:id', element: <PolicyDetailRoute /> },
          { path: 'access-control/lists/new', element: <ListCreateRoute /> },
          { path: 'access-control/lists/:id', element: <ListDetailRoute /> },
          { path: 'sync-tasks', element: <SyncTasksView /> },
          { path: 'node-issues', element: <NodeIssuesView /> },
          { path: 'diagnostics', element: <DiagnosticsView /> },
          { path: 'traffic', element: <TrafficView /> },
          { path: 'settings', element: <SettingsView /> },
          // A legal document's editor is a page under Settings (terms | privacy), so the sidebar keeps Settings lit.
          { path: 'settings/legal/:kind', element: <LegalEditorRoute /> },
          { path: 'language-packs', element: <LanguagePacksView /> },
          ...PLACEHOLDER_PATHS.map(p => ({ path: p, element: <PlaceholderView /> })),
          // An unknown /admin/... address: a not-found page in the shell, not a silent bounce to the dashboard.
          { path: '*', element: <AdminNotFound /> },
        ],
      },
    ],
  },
  {
    path: '/user',
    element: <RequireAuth />,
    children: [
      {
        element: <UserShellRoute />,
        children: [
          { index: true, element: <Navigate to="me" replace /> },
          { path: 'me', element: <MeView /> },
          { path: '*', element: <UserNotFound /> },
        ],
      },
    ],
  },
  // Mandatory-2FA enrollment gate. Authenticated but layout-less (full-screen);
  // the axios interceptor redirects here on a 2fa_enrollment_required 403.
  {
    path: '/enroll-2fa',
    element: <RequireAuth />,
    children: [{ index: true, element: <Enroll2FAView /> }],
  },
      // Catch-all outside /admin and /user: any unknown URL bounces to the role-based home.
      { path: '*', element: <RootRedirect /> },
    ],
  },
], { basename: panelPath || undefined })
