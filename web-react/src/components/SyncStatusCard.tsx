import { useEffect, useMemo, useRef, useState } from 'react'
import { Box, Button, Chip, CircularProgress, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { SYNC_STATUS_UNAVAILABLE_CODE, type SyncStatus } from '@/api/syncStatus'
import { useSyncStatus } from '@/query/syncStatus'
import { useQueryScope } from '@/query/useQueryScope'

/**
 * Shows what local sync work is observable for one user.
 *
 * Two things this deliberately does NOT say: that upstream is in sync (no
 * active task is evidence about the local queue only), and that a stopped
 * observation means anything settled. See ADR 0034.
 */
export default function SyncStatusCard({ userId }: { userId: number }) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()

  // Where this observation window started. Its only job is the wall-clock
  // budget — the window bounds what this screen does, not how long the backend
  // may retry.
  const startedAt = useRef(Date.now())
  const [budgetSpent, setBudgetSpent] = useState(false)

  const query = useSyncStatus(scope, userId, true)
  const status: SyncStatus | undefined = query.data

  const watching = !budgetSpent && status?.state !== 'no_active_tasks'

  useEffect(() => {
    if (!watching) return
    const remaining = 5 * 60_000 - (Date.now() - startedAt.current)
    if (remaining <= 0) { setBudgetSpent(true); return }
    const id = setTimeout(() => setBudgetSpent(true), remaining)
    return () => clearTimeout(id)
  }, [watching])

  const title = t('admin:sync_status.title', { defaultValue: '后台同步任务' })

  const failureText = useMemo(() => {
    if (!query.isError) return null
    const code = (query.error as { response?: { data?: { code?: string } } })?.response?.data?.code
    // An unknown state and an empty queue drive different UI; never collapse them.
    return code === SYNC_STATUS_UNAVAILABLE_CODE
      ? t('admin:sync_status.unknown', { defaultValue: '同步状态暂时未知' })
      : t('admin:sync_status.unknown', { defaultValue: '同步状态暂时未知' })
  }, [query.isError, query.error, t])

  if (query.isPending) {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant }}>{title}</Typography>
        <CircularProgress size={14} />
      </Box>
    )
  }

  if (failureText) {
    return (
      <Box>
        <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant, mb: 0.5 }}>{title}</Typography>
        <Typography sx={{ fontSize: 12, color: md.error }}>{failureText}</Typography>
      </Box>
    )
  }

  const active = status?.active_tasks ?? []
  const terminal = status?.recent_terminal_tasks ?? []

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 0.5 }}>
        <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant }}>{title}</Typography>
        {budgetSpent && watching === false && active.length > 0 && (
          <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
            {t('admin:sync_status.watch_paused', { defaultValue: '自动刷新已暂停' })}
          </Typography>
        )}
        <Button size="small" variant="text" onClick={() => { startedAt.current = Date.now(); setBudgetSpent(false); void query.refetch() }}>
          {t('admin:sync_status.refresh', { defaultValue: '刷新' })}
        </Button>
      </Box>

      {active.length === 0 ? (
        // Not "upstream synced" — only that nothing is queued locally.
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:sync_status.none', { defaultValue: '当前未发现待处理任务' })}
        </Typography>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25 }}>
          {active.map(task => (
            <Box key={task.id} sx={{ display: 'flex', alignItems: 'baseline', gap: 1, fontSize: 12 }}>
              <Chip size="small" label={task.status} sx={{ height: 20 }} />
              <span>{task.type}</span>
              {task.attempts > 0 && (
                <Typography component="span" sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
                  {t('admin:sync_status.attempts', { count: task.attempts, defaultValue: '重试 {{count}} 次' })}
                </Typography>
              )}
              {/* Only that an error was recorded — the text never leaves the server. */}
              {task.has_error && (
                <Typography component="span" sx={{ fontSize: 11, color: md.error }}>
                  {t('admin:sync_status.has_error', { defaultValue: '有错误记录' })}
                </Typography>
              )}
            </Box>
          ))}
          {status?.active_tasks_truncated && (
            <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant }}>
              {t('admin:sync_status.truncated', { defaultValue: '仅显示最近的部分任务' })}
            </Typography>
          )}
        </Box>
      )}

      {terminal.length > 0 && (
        <Box sx={{ mt: 1 }}>
          <Typography sx={{ fontSize: 11, color: md.onSurfaceVariant, mb: 0.25 }}>
            {t('admin:sync_status.recent', { defaultValue: '最近结果（仅保留的记录）' })}
          </Typography>
          {terminal.map(task => (
            <Box key={task.id} sx={{ display: 'flex', alignItems: 'baseline', gap: 1, fontSize: 11, color: md.onSurfaceVariant }}>
              <span>{task.type}</span>
              <span>{task.status}</span>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}
