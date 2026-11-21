import type { Tone } from '@/components/common/tones'
import type { SyncTask, SyncTaskStatus, SyncTaskType } from '@/api/types'

// The backend serializes some paths in PascalCase and some in snake_case (see SyncTask in api/types);
// these read either.
export const idOf = (r: SyncTask) => r.id ?? r.ID ?? 0
export const typeOf = (r: SyncTask) => (r.type ?? r.Type ?? '') as SyncTaskType | ''
export const statusOf = (r: SyncTask) => (r.status ?? r.Status ?? '') as SyncTaskStatus | ''
export const summaryOf = (r: SyncTask) => r.summary ?? r.Summary ?? ''
export const attemptsOf = (r: SyncTask) => r.attempts ?? r.Attempts ?? 0
export const nextRunOf = (r: SyncTask) => r.next_run_at ?? r.NextRunAt
export const lastErrorOf = (r: SyncTask) => r.last_error ?? r.LastError ?? ''
export const createdOf = (r: SyncTask) => r.created_at ?? r.CreatedAt
export const updatedOf = (r: SyncTask) => r.updated_at ?? r.UpdatedAt
export const finishedOf = (r: SyncTask) => r.finished_at ?? r.FinishedAt

export function targetOf(r: SyncTask): string {
  const ty = r.target_type ?? r.TargetType ?? ''
  const id = r.target_id ?? r.TargetID ?? ''
  return `${ty}#${id}`
}

export const STATUSES: SyncTaskStatus[] = ['pending', 'running', 'succeeded', 'canceled', 'retired']
export const TYPES: SyncTaskType[] = [
  'user_delete', 'user_resync', 'user_push_config',
  'node_create', 'node_delete', 'node_set_enabled', 'node_update',
]

/** A retired task belongs to a backend that has been migrated away: it can be read and purged, never acted on. */
export const isRetired = (r: SyncTask) => statusOf(r) === 'retired'

/** Waiting for a retry is the one state that needs attention; a finished or retired task is quiet. */
export const STATUS_TONE: Record<SyncTaskStatus, Tone> = {
  pending: 'warning',
  running: 'info',
  succeeded: 'success',
  canceled: 'neutral',
  retired: 'neutral',
}
