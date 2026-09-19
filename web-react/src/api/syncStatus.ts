import { client } from './client'
import type { ReadOptions } from './requestOptions'

/**
 * What a status read observed about LOCAL sync work. There is deliberately no
 * value meaning "upstream is in sync": no active task is evidence about the
 * queue, not about whether a panel applied the configuration. See ADR 0034.
 */
export type SyncStatusState = 'active_tasks' | 'no_active_tasks'

/** The subset of a task the API exposes. Payload, summary and the raw upstream
 *  error are not sent — a payload can carry subscription credentials. */
export interface SyncTaskView {
  id: number
  type: string
  status: string
  attempts: number
  next_run_at: string
  created_at: string
  updated_at: string
  finished_at?: string
  /** Only that an error was recorded; never the text. */
  has_error: boolean
}

export interface SyncStatus {
  target_type: string
  target_id: number
  target_exists: boolean
  /** When the server read its own task store. Says nothing about the panel. */
  observed_at: string
  covered_task_types: string[]
  state: SyncStatusState
  active_tasks: SyncTaskView[]
  /** The lists are capped, so a count read off them is a floor, not a total. */
  active_tasks_truncated: boolean
  recent_terminal_tasks: SyncTaskView[]
  history_truncated: boolean
  /** Always 'retained_only': purged rows are absent, and absence must not be
   *  read as "never ran". */
  history_scope: string
}

/** The stable code the server returns when it could not answer. */
export const SYNC_STATUS_UNAVAILABLE_CODE = 'sync_status_unavailable'

export async function getSyncStatus(userId: number, opts: ReadOptions = {}): Promise<SyncStatus> {
  const { data } = await client.get<SyncStatus>(`/admin/users/${userId}/sync-status`, {
    signal: opts.signal,
  })
  return data
}

/** The self-service read: the target is the session, never a parameter. */
export async function getMySyncStatus(opts: ReadOptions = {}): Promise<SyncStatus> {
  const { data } = await client.get<SyncStatus>('/user/me/sync-status', { signal: opts.signal })
  return data
}
