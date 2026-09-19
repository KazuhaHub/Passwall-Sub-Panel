import type { AuditFilter } from '@/api/audit'
import type { AuthEventFilter } from '@/api/authEvents'
import type { EmailLogFilter } from '@/api/emailLogs'
import type { GroupListParams } from '@/api/groups'
import type { NodeListParams } from '@/api/nodes'
import type { RuleSetListParams } from '@/api/rules'
import type { ServerListParams } from '@/api/servers'
import type { SubLogFilter } from '@/api/subLogs'
import type { SyncTaskListParams } from '@/api/syncTasks'
import type { TemplateListParams } from '@/api/templates'
import type { TrafficHistoryParams } from '@/api/traffic'
import type { UserListParams } from '@/api/users'
import type { QueryScope } from './session'

/**
 * Query key factory. Every private key starts with the session scope, so
 * invalidating one session's data can never reach another's — and a cached
 * body produced for an admin is unreachable once the session becomes an
 * operator (the same URL is redacted differently per caller).
 *
 * Only resources that have actually been migrated get an entry here; adding a
 * key for a view that still fetches by hand would create a second, unread
 * copy of the same data.
 *
 * The params object in a list key must carry every argument that can change the
 * response (page, page_size, keyword, sort, filters). Anything the fetcher
 * closes over but the key omits is a stale-data bug waiting to happen.
 */
const privateRoot = (s: QueryScope) => ['private', s] as const

export const alertKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'alerts'] as const,
}

export const userKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'users'] as const,
  lists: (s: QueryScope) => [...userKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: UserListParams) => [...userKeys.lists(s), params] as const,
}

export const trafficKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'traffic'] as const,
  top: (s: QueryScope, limit: number) => [...trafficKeys.all(s), 'top', limit] as const,
  /** A panel-wide traffic trend window. Range + timezone are part of the key. */
  history: (s: QueryScope, params: TrafficHistoryParams) => [...trafficKeys.all(s), 'history', params] as const,
  /** The node-scoped rank leaderboard. */
  topNodes: (s: QueryScope, limit: number) => [...trafficKeys.all(s), 'top-nodes', limit] as const,
  /** One user's per-node usage breakdown (Traffic page, Trend tab). */
  userNodes: (s: QueryScope, userId: number) => [...trafficKeys.all(s), 'user', userId, 'nodes'] as const,
  /** One user's per-server usage breakdown. */
  userServers: (s: QueryScope, userId: number) => [...trafficKeys.all(s), 'user', userId, 'servers'] as const,
}

export const dashboardKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'dashboard'] as const,
  summary: (s: QueryScope) => [...dashboardKeys.all(s), 'summary'] as const,
}

export const nodeKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'nodes'] as const,
  lists: (s: QueryScope) => [...nodeKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: NodeListParams) => [...nodeKeys.lists(s), params] as const,
  /** The interleaved separator rows shown among the nodes. */
  separators: (s: QueryScope) => [...nodeKeys.all(s), 'separators'] as const,
  /** Inbounds on a panel that PSP does not manage, for one panel. */
  unmanaged: (s: QueryScope, panelId: number) => [...nodeKeys.all(s), 'unmanaged', panelId] as const,
}

/** Certificate issuance/renewal activity (the Logs page's Certificates tab). */
export const certKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'certs'] as const,
  events: (s: QueryScope, page: number, pageSize: number) =>
    [...certKeys.all(s), 'events', page, pageSize] as const,
  /** The Certificates page's three readers. */
  list: (s: QueryScope) => [...certKeys.all(s), 'list'] as const,
  creds: (s: QueryScope) => [...certKeys.all(s), 'creds'] as const,
  accounts: (s: QueryScope) => [...certKeys.all(s), 'accounts'] as const,
}

/** User groups. A dictionary read: written rarely, read by several views. */
export const groupKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'groups'] as const,
  lists: (s: QueryScope) => [...groupKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: GroupListParams) => [...groupKeys.lists(s), params] as const,
}

/**
 * The global UI settings blob. One key: every admin settings page edits a slice
 * of the same record, so they must all read the same entry — a per-page key
 * would let two pages hold divergent copies of one record.
 */
export const settingsKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'settings'] as const,
  ui: (s: QueryScope) => [...settingsKeys.all(s), 'ui'] as const,
  mail: (s: QueryScope) => [...settingsKeys.all(s), 'mail'] as const,
  saml: (s: QueryScope) => [...settingsKeys.all(s), 'saml'] as const,
  oidc: (s: QueryScope) => [...settingsKeys.all(s), 'oidc'] as const,
}

/** YAML-backed rule sets (`<ConfigDir>/rulesets/`). */
export const ruleKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'rules'] as const,
  lists: (s: QueryScope) => [...ruleKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: RuleSetListParams) => [...ruleKeys.lists(s), params] as const,
}

/** YAML-backed subscription templates (`<ConfigDir>/templates/`). */
export const templateKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'templates'] as const,
  lists: (s: QueryScope) => [...templateKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: TemplateListParams) => [...templateKeys.lists(s), params] as const,
}

/** Uploaded runtime language packs (`<ConfigDir>/locales/`). */
export const localeKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'locales'] as const,
  list: (s: QueryScope) => [...localeKeys.all(s), 'list'] as const,
}

export const serverKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'servers'] as const,
  lists: (s: QueryScope) => [...serverKeys.all(s), 'list'] as const,
  list: (s: QueryScope, params: ServerListParams) => [...serverKeys.lists(s), params] as const,
}

export const authEventKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'auth-events'] as const,
  /** The admin auth-log page, with its own filters. */
  list: (s: QueryScope, filter: AuthEventFilter) => [...authEventKeys.all(s), 'list', filter] as const,
  /** One user's slice of the sign-in log, as shown by the edit dialog. */
  forUser: (s: QueryScope, userId: number, pageSize: number) =>
    [...authEventKeys.all(s), 'user', userId, pageSize] as const,
}

/** The Logs page's per-tab lists. Each tab has its own filters and paging. */
export const logKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'logs'] as const,
  subs: (s: QueryScope, filter: SubLogFilter) => [...logKeys.all(s), 'sub', filter] as const,
  audit: (s: QueryScope, filter: AuditFilter) => [...logKeys.all(s), 'audit', filter] as const,
  email: (s: QueryScope, filter: EmailLogFilter) => [...logKeys.all(s), 'email', filter] as const,
}

/** The caller's own self-service reads (`/user/me/*`). */
export const meKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'me'] as const,
  profile: (s: QueryScope) => [...meKeys.all(s), 'profile'] as const,
  usage: (s: QueryScope) => [...meKeys.all(s), 'usage'] as const,
}

/** Concurrent-location verdicts, as shown on the Geo anomalies tab. */
export const geoAnomalyKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'geo-anomalies'] as const,
}

/** The upstream sync-task queue, as shown on the Sync tasks page. */
export const syncTaskKeys = {
  all: (s: QueryScope) => [...privateRoot(s), 'sync-tasks'] as const,
  list: (s: QueryScope, params: SyncTaskListParams) => [...syncTaskKeys.all(s), 'list', params] as const,
}
