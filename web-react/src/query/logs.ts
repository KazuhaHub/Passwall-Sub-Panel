import { queryOptions, useQuery } from '@tanstack/react-query'
import { listAudit, type AuditEntry } from '@/api/audit'
import { listAuthEvents, type AuthEvent, type AuthEventFilter } from '@/api/authEvents'
import { getEmailLogs, type EmailLog, type EmailLogFilter } from '@/api/emailLogs'
import { getSubLogs, type SubLog, type SubLogFilter } from '@/api/subLogs'
import { authEventKeys, logKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The Logs page's four tab lists. Each is its own query, keyed by that tab's
 * complete filter set — a tab is only mounted while it is the active one, so
 * these are read on demand and never in the background.
 */
export interface LogPage<T> {
  items: T[]
  total: number
}

export function subLogsQuery(scope: QueryScope, filter: SubLogFilter) {
  return queryOptions({
    queryKey: logKeys.subs(scope, filter),
    queryFn: ({ signal }): Promise<LogPage<SubLog>> => getSubLogs(filter, { signal }),
    ...freshness(policies.logList),
  })
}

export function useSubLogs(scope: QueryScope, filter: SubLogFilter) {
  return useQuery(subLogsQuery(scope, filter))
}

export function auditLogQuery(scope: QueryScope, filter: Parameters<typeof listAudit>[0]) {
  return queryOptions({
    queryKey: logKeys.audit(scope, filter ?? {}),
    queryFn: ({ signal }): Promise<LogPage<AuditEntry>> => listAudit(filter, { signal }),
    ...freshness(policies.logList),
  })
}

export function useAuditLog(scope: QueryScope, filter: Parameters<typeof listAudit>[0]) {
  return useQuery(auditLogQuery(scope, filter))
}

export function authLogQuery(scope: QueryScope, filter: AuthEventFilter) {
  return queryOptions({
    queryKey: authEventKeys.list(scope, filter),
    queryFn: ({ signal }): Promise<LogPage<AuthEvent>> => listAuthEvents(filter, { signal }),
    ...freshness(policies.logList),
  })
}

export function useAuthLog(scope: QueryScope, filter: AuthEventFilter) {
  return useQuery(authLogQuery(scope, filter))
}

export function emailLogQuery(scope: QueryScope, filter: EmailLogFilter) {
  return queryOptions({
    queryKey: logKeys.email(scope, filter),
    queryFn: ({ signal }): Promise<LogPage<EmailLog>> => getEmailLogs(filter, { signal }),
    ...freshness(policies.logList),
  })
}

export function useEmailLog(scope: QueryScope, filter: EmailLogFilter) {
  return useQuery(emailLogQuery(scope, filter))
}
