import { useEffect, useState } from 'react'
import { Box, CircularProgress, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { listAuthEvents, type AuthEvent } from '@/api/authEvents'
import { formatRegion } from '@/utils/geo'

/** UserActivity shows a user's recent sign-in events (a per-user slice of the
 *  authentication log) inside the admin user-edit dialog. Read-only; fetches
 *  the latest few auth_events for the user when mounted. */
export function UserActivity({ userId }: { userId: number }) {
  const { t } = useTranslation('admin')
  const md = useTheme().palette.md
  const [items, setItems] = useState<AuthEvent[] | null>(null)

  useEffect(() => {
    let alive = true
    setItems(null)
    listAuthEvents({ user_id: userId, page_size: 8 })
      .then(r => { if (alive) setItems(r.items) })
      .catch(() => { if (alive) setItems([]) })
    return () => { alive = false }
  }, [userId])

  return (
    <Box sx={{ mt: 1 }}>
      <Typography sx={{ fontSize: 13, fontWeight: 600, color: md.onSurfaceVariant, mb: 0.75 }}>
        {t('users.activity.title', { defaultValue: '最近登录' })}
      </Typography>
      {items === null && <CircularProgress size={18} />}
      {items !== null && items.length === 0 && (
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('users.activity.empty', { defaultValue: '暂无登录记录' })}
        </Typography>
      )}
      {items !== null && items.length > 0 && (
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          {items.map(r => (
            <Box key={r.id} sx={{ fontSize: 12, display: 'flex', flexDirection: 'column', gap: 0.25, py: 0.5, borderTop: `1px solid ${md.outlineVariant}` }}>
              <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: 1 }}>
                <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.75, minWidth: 0 }}>
                  <Box component="span" sx={{ fontWeight: 600, color: r.outcome === 'success' ? '#1b5e20' : '#b00020' }}>
                    {r.outcome === 'success'
                      ? t('users.activity.ok', { defaultValue: '成功' })
                      : t('users.activity.fail', { defaultValue: '失败' })}
                  </Box>
                  <Box component="span" sx={{ textTransform: 'uppercase', color: md.onSurfaceVariant, fontSize: 11 }}>{r.method}</Box>
                </Box>
                <Box component="span" sx={{ color: md.onSurfaceVariant, whiteSpace: 'nowrap', fontSize: 11 }}>
                  {new Date(r.at).toLocaleString()}
                </Box>
              </Box>
              <Box component="span" sx={{ color: md.onSurfaceVariant, wordBreak: 'break-all' }}>
                {r.ip}{formatRegion(r.region) ? ` · ${formatRegion(r.region)}` : ''}{r.reason ? ` · ${r.reason}` : ''}
              </Box>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}
