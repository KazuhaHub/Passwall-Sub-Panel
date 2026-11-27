import type { SyncTask } from '@/api/types'
import { StatusBadge } from '@/components/common/StatusBadge'
import { useAdminT } from '../logs/shared'
import { STATUS_TONE, attemptsOf, statusOf } from './model'

/**
 * The state in words, and for a task still waiting, why: it has been tried N times and is queued for
 * the next attempt, or it has not run yet. A healthy state is a quiet dot; waiting is the one that
 * says more.
 */
export function StatusCell({ task, reason = true }: { task: SyncTask; reason?: boolean }) {
  const t = useAdminT()
  const status = statusOf(task)
  if (!status) return <span>—</span>
  const attempts = attemptsOf(task)
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <StatusBadge tone={STATUS_TONE[status]} label={t(`sync_tasks.status.${status}`)} />
      {reason && status === 'pending' && (
        <span className="truncate text-xs text-muted-foreground">
          {attempts > 0 ? t('sync_tasks.reason.waiting_retry', { count: attempts }) : t('sync_tasks.reason.not_started')}
        </span>
      )}
    </div>
  )
}
