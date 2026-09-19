import { useEffect, useState } from 'react'
import { Box, Button, Chip, CircularProgress, Typography, useTheme } from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import type { SyncStatus } from '@/api/syncStatus'
import { syncStatusKeys } from '@/query/keys'
import { syncStatusErrorStatus, useObservationWindow, useSyncStatus } from '@/query/syncStatus'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'

/** Statuses that are an answer rather than a failure. Neither changes on its
 *  own, so neither is worth another nineteen reads. */
const REFUSAL_STATUSES = [403, 404]

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
  const md = useTheme().palette.md
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

  const active = status?.active_tasks ?? []
  const terminal = status?.recent_terminal_tasks ?? []
  // Every answer carries when the server read its own store. Shown whenever
  // there is a snapshot on screen, so a retained result is never read as the
  // current state.
  const observedAt = status?.observed_at ? formatDualTz(status.observed_at, panelTz) : null

  return (
    <Box sx={{ mt: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1 }}>
        <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant }}>
          {t('sync_status.title', { defaultValue: '后台同步任务' })}
        </Typography>
        <Button
          size="small"
          variant="text"
          sx={{ minWidth: 0, fontSize: 11, py: 0 }}
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
      </Box>

      {refused && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('sync_status.target_unavailable', { defaultValue: '目标已不可查询' })}
        </Typography>
      )}

      {!refused && query.isPending && <CircularProgress size={18} />}

      {/* A failed read with nothing to fall back on is unknown — never "no
          pending tasks", which is a claim about the store. */}
      {!refused && query.isError && !status && (
        <Typography sx={{ fontSize: 12, color: md.error }}>
          {t('sync_status.unknown', { defaultValue: '同步状态暂时未知' })}
        </Typography>
      )}

      {!refused && status && (
        <>
          {active.length === 0 ? (
            // Not "upstream synced" — only that nothing is queued locally.
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
              {t('sync_status.none', { defaultValue: '当前未发现待处理任务' })}
            </Typography>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25 }}>
              {active.map(task => (
                <Box key={task.id} sx={{ display: 'flex', alignItems: 'baseline', gap: 0.75, fontSize: 12 }}>
                  <Chip size="small" label={t(`sync_tasks.status.${task.status}`, { defaultValue: task.status })} sx={{ height: 20 }} />
                  <span>{t(`sync_tasks.type.${task.type}`, { defaultValue: task.type })}</span>
                  {task.attempts > 0 && (
                    <Typography component="span" sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                      {t('sync_status.attempts', { count: task.attempts, defaultValue: '重试 {{count}} 次' })}
                    </Typography>
                  )}
                  {/* Only that an error was recorded. The text can quote an
                      upstream endpoint or token, so it never leaves the server:
                      it is not in the DTO at all. */}
                  {task.has_error && (
                    <Typography component="span" sx={{ fontSize: 11, color: md.error }}>
                      {t('sync_status.has_error', { defaultValue: '有执行错误记录，任务待重试' })}
                    </Typography>
                  )}
                </Box>
              ))}
              {status.active_tasks_truncated && (
                <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                  {t('sync_status.truncated', { defaultValue: '仅显示最近的部分任务' })}
                </Typography>
              )}
            </Box>
          )}

          {terminal.length > 0 && (
            <Box sx={{ mt: 0.75 }}>
              <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                {/* Absence from this list is not evidence a task never ran. */}
                {t('sync_status.recent', { defaultValue: '最近结果（仅保留的记录）' })}
              </Typography>
              {terminal.map(task => (
                <Box key={task.id} sx={{ display: 'flex', alignItems: 'baseline', gap: 0.75, fontSize: 11, color: md.onSurfaceVariant }}>
                  <span>{t(`sync_tasks.type.${task.type}`, { defaultValue: task.type })}</span>
                  <span>{t(`sync_tasks.status.${task.status}`, { defaultValue: task.status })}</span>
                </Box>
              ))}
            </Box>
          )}
        </>
      )}

      {/* Both of these mean the snapshot above is historical, and say so. */}
      {!refused && query.isError && status && (
        <Typography sx={{ fontSize: 11, color: md.error, mt: 0.5 }}>
          {t('sync_status.read_failed', { defaultValue: '读取失败，以下为上次观察结果，可能已经变化' })}
        </Typography>
      )}
      {!refused && watch.expired && !query.isError && (
        <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.5 }}>
          {t('sync_status.watch_paused', { defaultValue: '自动刷新已暂停，以下为上次观察结果，可能已经变化' })}
        </Typography>
      )}
      {!refused && observedAt && (
        <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, mt: 0.5 }}>
          {t('sync_status.observed_at', { defaultValue: '最后观察时间：{{time}}', time: observedAt })}
        </Typography>
      )}
    </Box>
  )
}
