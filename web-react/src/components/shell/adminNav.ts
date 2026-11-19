import {
  IconApps, IconCert, IconDashboard, IconDiagnostics, IconGroups, IconIssue, IconLanguage, IconLogs, IconNode,
  IconPolicy, IconRules, IconServer, IconSettings, IconShield, IconSync, IconTemplates, IconTraffic, IconUsers,
} from '@/lib/icons'
import type { NavSection } from './nav'

// The admin console's navigation, as the data the shell takes. Same entries, order,
// grouping and `adminOnly` flags as ADMIN_NAV in layouts/AdminLayout.tsx, which this
// replaces when the shell is mounted; nav.test.ts reads that file and fails if
// the two disagree, so the swap cannot change who sees what.
export const ADMIN_NAV: NavSection[] = [
  { items: [
    { to: '/admin/dashboard', labelKey: 'nav:admin.dashboard', icon: IconDashboard },
  ] },
  { labelKey: 'nav:section.directory', items: [
    { to: '/admin/users', labelKey: 'nav:admin.users', icon: IconUsers },
    { to: '/admin/groups', labelKey: 'nav:admin.groups', icon: IconGroups },
  ] },
  { labelKey: 'nav:section.infrastructure', items: [
    { to: '/admin/servers', labelKey: 'nav:admin.servers', icon: IconServer, adminOnly: true },
    { to: '/admin/nodes', labelKey: 'nav:admin.nodes', icon: IconNode },
    { to: '/admin/certs', labelKey: 'nav:admin.certs', icon: IconCert, adminOnly: true },
  ] },
  { labelKey: 'nav:section.subscription', items: [
    { to: '/admin/rules', labelKey: 'nav:admin.rules', icon: IconRules },
    { to: '/admin/templates', labelKey: 'nav:admin.templates', icon: IconTemplates },
    { to: '/admin/sub-clients', labelKey: 'nav:admin.sub_clients', icon: IconApps, adminOnly: true },
  ] },
  { labelKey: 'nav:section.reporting', items: [
    { to: '/admin/traffic', labelKey: 'nav:admin.traffic', icon: IconTraffic },
    { to: '/admin/logs', labelKey: 'nav:admin.logs', icon: IconLogs },
    { to: '/admin/sync-tasks', labelKey: 'nav:admin.sync_tasks', icon: IconSync },
    { to: '/admin/node-issues', labelKey: 'nav:admin.node_issues', icon: IconIssue },
    { to: '/admin/diagnostics', labelKey: 'nav:admin.diagnostics', icon: IconDiagnostics, adminOnly: true },
  ] },
  // Standalone and admin-only everywhere it is named (here, ADMIN_ONLY_ROUTES and the
  // risk.view / access.view capabilities): every read behind both pages is adminGroup.
  // Access control sits here and not under infrastructure because its subject is a
  // person's reach, not the fleet's plumbing.
  { items: [
    { to: '/admin/risk', labelKey: 'nav:admin.risk_center', icon: IconShield, adminOnly: true },
    { to: '/admin/access-control', labelKey: 'nav:admin.access_control', icon: IconPolicy, adminOnly: true },
  ] },
  { items: [
    { to: '/admin/language-packs', labelKey: 'nav:admin.language_packs', icon: IconLanguage, adminOnly: true },
    { to: '/admin/settings', labelKey: 'nav:admin.settings', icon: IconSettings, adminOnly: true },
  ] },
]
