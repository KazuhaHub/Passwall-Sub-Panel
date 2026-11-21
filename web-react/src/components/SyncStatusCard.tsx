import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import type { SyncStatus } from '@/api/syncStatus'
import { observeUser, onPendingForUser } from '@/api/syncPending'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Button } from '@/components/ui/button'
import { syncStatusKeys } from '@/query/keys'
import { syncStatusErrorStatus, useObservationWindow, useSyncStatus } from '@/query/syncStatus'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'

/** Statuses that are an answer rather than a failure. Neither changes on its
 *  own, so neither is worth another nineteen reads. */
const REFUSAL_STATUSES = [403, 404]

/** A task still queued is neutral; one being worked is a notice; a failed one is a failure. The words are always shown too. */
const TASK_TONE: Record<string, Tone> = { pending: 'neutral', running: 'info', failed: 'danger' }

/**
 * SyncStatusCard shows what LOCAL sync work is observable for one user: what is
 * still queued for them and whether it is retrying.
 *
 * Three things it deliberately does not say, because the read cannot support
 * them (ADR 0034):
 *
 *  - That upstream is in sync. An empty queue is evidence about PSP's task
 *    store, never about whether a panel applied the configuration.
 *  - That a stopped observation means anything settled. The window bounds what
 *    this screen does; the backend may retry long past it.
 *  - That a failed read is an empty queue. It is an unknown state, and the two
 *    drive different UI.
 *
 * Anything rendered while the window is closed or the last read failed is a
 * snapshot of a past observation, and is labelled with its time as one.
 */
export default function SyncStatusCard({ userId }: { userId: number }) {
  const { t } = useTranslation('admin')
  const panelTz = useSiteStore(s => s.timezone)
  const scope = useQueryScope()
  const queryClient = useQueryClient()

  const watch = useObservationWindow()
  const query = useSyncStatus(scope, userId, watch)
  const status: SyncStatus | undefined = query.data

  const [refused, setRefused] = useState(false)

  // A refusal is latched rather than read off the live query throughout: the
  // cache is dropped below, which resets the query's error, and the message has
  // to outlive that. Switching target clears it — a different target is a
  // different access decision.
  useEffect(() => { setRefused(false) }, [userId])

  useEffect(() => {
    const s = syncStatusErrorStatus(query.error)
    if (s === undefined || !REFUSAL_STATUSES.includes(s)) return
    // A 403 or 404 is a verdict on this caller's access to this target, not a
    // transient failure, and it will not clear on its own for us to keep
    // watching (ADR 0034, frontend §7).
    setRefused(true)
    watch.stop()
  }, [query.error, watch.stop])

  useEffect(() => {
    // Only once the window has actually closed: removing the entry while the
    // query is still enabled would recreate it and fire one more request at an
    // endpoint that just refused us.
    if (!refused || watch.watching) return
    queryClient.removeQueries({ queryKey: syncStatusKeys.user(scope, userId) })
  }, [refused, watch.watching, queryClient, scope, userId])

  useEffect(() => {
    // Claims this target while the area is on screen. Two things depend on it:
    // a write reporting queued work finds the area to hand it to, and the
    // transport skips its toast because this area already says the same thing.
    const release = observeUser(userId)
    const offPending = onPendingForUser(id => {
      if (id !== userId) return
      // A save in this dialog queued upstream work. Start a new window now
      // rather than leaving the admin to reopen the dialog to see it.
      setRefused(false)
      watch.restart()
      void query.refetch()
    })
    return () => { release(); offPending() }
  }, [userId, watch.restart, query.refetch])

  const active = status?.active_tasks ?? []
  const terminal = status?.recent_terminal_tasks ?? []
  // Every answer carries when the server read its own store. Shown whenever
  // there is a snapshot on screen, so a retained result is never read as the
  // current state.
  const observedAt = status?.observed_at ? formatDualTz(status.observed_at, panelTz) : null

  return (
    <section className="mt-2 space-y-1" aria-label={t('sync_status.title', { defaultValue: '后台同步任务' })}>
      <div className="flex items-center gap-1">
        <h3 className="text-[0.8125rem] font-semibold text-muted-foreground">
          {t('sync_status.title', { defaultValue: '后台同步任务' })}
        </h3>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            // A new observation window, not just a read: the budget starts over.
            // Clearing the refusal is what makes this an actual retry — an admin
            // whose access was restored should not have to reopen the dialog.
            setRefused(false)
            watch.restart()
            void query.refetch()
          }}
        >
          {t('sync_status.refresh', { defaultValue: '刷新' })}
        </Button>
      </div>

      {refused && (
        <p className="text-xs text-muted-foreground">
          {t('sync_status.target_unavailable', { defaultValue: '目标已不可查询' })}
        </p>
      )}

      {!refused && query.isPending && <CardSkeleton lines={2} />}

      {/* A failed read with nothing to fall back on is unknown — never "no
          pending tasks", which is a claim about the store. */}
      {!refused && query.isError && !status && (
        <p className="text-xs text-destructive">
          {t('sync_status.unknown', { defaultValue: '同步状态暂时未知' })}
        </p>
      )}

      {!refused && status && (
        <>
          {active.length === 0 ? (
            // Not "upstream synced" — only that nothing is queued locally.
            <p className="text-xs text-muted-foreground">
              {t('sync_status.none', { defaultValue: '当前未发现待处理任务' })}
            </p>
          ) : (
            <ul className="flex flex-col gap-1">
              {active.map(task => (
                <li key={task.id} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
                  <StatusBadge tone={TASK_TONE[task.status] ?? 'neutral'} label={t(`sync_tasks.status.${task.status}`, { defaultValue: task.status })} />
                  <span>{t(`sync_tasks.type.${task.type}`, { defaultValue: task.type })}</span>
                  {task.attempts > 0 && (
                    <span className="text-muted-foreground">
                      {t('sync_status.attempts', { count: task.attempts, defaultValue: '重试 {{count}} 次' })}
                    </span>
                  )}
                  {/* Only that an error was recorded. The text can quote an
                      upstream endpoint or token, so it never leaves the server:
                      it is not in the DTO at all. */}
                  {task.has_error && (
                    <span className="text-destructive">
                      {t('sync_status.has_error', { defaultValue: '有执行错误记录，任务待重试' })}
                    </span>
                  )}
                </li>
              ))}
              {status.active_tasks_truncated && (
                <li className="text-xs text-muted-foreground">
                  {t('sync_status.truncated', { defaultValue: '仅显示最近的部分任务' })}
                </li>
              )}
            </ul>
          )}

          {terminal.length > 0 && (
            <div className="pt-1.5">
              <p className="text-xs text-muted-foreground">
                {/* Absence from this list is not evidence a task never ran. */}
                {t('sync_status.recent', { defaultValue: '最近结果（仅保留的记录）' })}
              </p>
              <ul>
                {terminal.map(task => (
                  <li key={task.id} className="flex items-baseline gap-2 text-xs text-muted-foreground">
                    <span>{t(`sync_tasks.type.${task.type}`, { defaultValue: task.type })}</span>
                    <span>{t(`sync_tasks.status.${task.status}`, { defaultValue: task.status })}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </>
      )}

      {/* Both of these mean the snapshot above is historical, and say so. */}
      {!refused && query.isError && status && (
        <p className="text-xs text-destructive">
          {t('sync_status.read_failed', { defaultValue: '读取失败，以下为上次观察结果，可能已经变化' })}
        </p>
      )}
      {!refused && watch.expired && !query.isError && (
        <p className="text-xs text-muted-foreground">
          {t('sync_status.watch_paused', { defaultValue: '自动刷新已暂停，以下为上次观察结果，可能已经变化' })}
        </p>
      )}
      {!refused && observedAt && (
        <p className="text-xs text-muted-foreground">
          {t('sync_status.observed_at', { defaultValue: '最后观察时间：{{time}}', time: observedAt })}
        </p>
      )}
    </section>
  )
}
