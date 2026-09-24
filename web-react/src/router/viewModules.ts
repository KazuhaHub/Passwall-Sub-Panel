// ONE LOADER PER NAVIGABLE VIEW, SHARED BY React.lazy AND BY PREFETCHING.
//
// The router lazy-loads each view from these functions, and the sidebar warms
// the same functions ahead of a click. Sharing them is what makes a prefetch
// count: a dynamic import of the same module resolves to the same module record,
// so the chunk the prefetch fetched is the chunk React.lazy renders.
export type ViewLoader = () => Promise<unknown>

export const viewLoaders = {
  '/admin/dashboard': () => import('@/views/admin/DashboardView'),
  '/admin/servers': () => import('@/views/admin/ServersView'),
  '/admin/certs': () => import('@/views/admin/CertificatesView'),
  '/admin/groups': () => import('@/views/admin/GroupsView'),
  '/admin/users': () => import('@/views/admin/UsersView'),
  '/admin/nodes': () => import('@/views/admin/NodesView'),
  '/admin/rules': () => import('@/views/admin/RuleSetsView'),
  '/admin/templates': () => import('@/views/admin/TemplatesView'),
  '/admin/sub-clients': () => import('@/views/admin/SubClientsView'),
  '/admin/logs': () => import('@/views/admin/LogsView'),
  '/admin/sync-tasks': () => import('@/views/admin/SyncTasksView'),
  '/admin/node-issues': () => import('@/views/admin/NodeIssuesView'),
  '/admin/diagnostics': () => import('@/views/admin/DiagnosticsView'),
  '/admin/traffic': () => import('@/views/admin/TrafficView'),
  '/admin/settings': () => import('@/views/admin/SettingsView'),
  '/admin/language-packs': () => import('@/views/admin/LanguagePacksView'),
  '/user/me': () => import('@/views/user/MeView'),
} satisfies Record<string, ViewLoader>
