import { useMemo, useState, type MouseEvent } from 'react'
import {
  Badge,
  Box,
  CircularProgress,
  Divider,
  IconButton,
  ListItemIcon,
  Menu,
  MenuItem,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import NotificationsNoneIcon from '@mui/icons-material/NotificationsNone'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlined'
import ScheduleIcon from '@mui/icons-material/Schedule'
import SystemUpdateAltIcon from '@mui/icons-material/SystemUpdateAlt'
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { Alert, AlertSeverity, AlertType } from '@/api/alerts'
import { useAlerts } from '@/query/alerts'
import { useQueryScope } from '@/query/useQueryScope'

// In-app deep links per alert type. psp_upgrade is intentionally absent — it opens
// the GitHub releases page externally (handled in go()).
const ROUTE: Partial<Record<AlertType, string>> = {
  node_health: '/admin/nodes',
  cert_failed: '/admin/certs',
  cert_expiring: '/admin/certs',
  panel_upgrade: '/admin/servers',
  login_security: '/admin/logs',
}

const PSP_RELEASES_URL = 'https://github.com/KazuhaHub/passwall-sub-panel/releases'

const SEVERITY_RANK: Record<AlertSeverity, number> = { error: 0, warning: 1, info: 2 }

function typeIcon(type: AlertType, severity: AlertSeverity) {
  switch (type) {
    case 'cert_failed':
      return <ErrorOutlineIcon fontSize="small" />
    case 'cert_expiring':
      return <ScheduleIcon fontSize="small" />
    case 'panel_upgrade':
    case 'psp_upgrade':
      return <SystemUpdateAltIcon fontSize="small" />
    case 'login_security':
      return <ShieldOutlinedIcon fontSize="small" />
    case 'node_health':
    default:
      return severity === 'error' ? <ErrorOutlineIcon fontSize="small" /> : <WarningAmberIcon fontSize="small" />
  }
}

export default function NotificationBell() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin'])
  const navigate = useNavigate()

  const scope = useQueryScope()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const { data, isFetching, isError, refetch } = useAlerts(scope)

  // Sort a copy for display. Sorting the cached array in place would mutate
  // what every other observer of this query key reads.
  const alerts = useMemo(
    () => [...(data?.alerts ?? [])].sort((a, b) => SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity]),
    [data],
  )

  const total = alerts.length
  const highest: AlertSeverity = alerts.some(a => a.severity === 'error')
    ? 'error'
    : alerts.some(a => a.severity === 'warning') ? 'warning' : 'info'
  const badgeColor: 'error' | 'warning' | 'info' = highest

  const severityColor = (s: AlertSeverity) => (s === 'error' ? md.error : s === 'warning' ? md.tertiary : md.primary)

  function alertTitle(a: Alert): string {
    const name = a.target_name || ''
    switch (a.type) {
      case 'node_health':
        return t('alerts.title.node_health', { name, defaultValue: `节点 ${name} 异常` })
      case 'cert_failed':
        return t('alerts.title.cert_failed', { name, defaultValue: `证书 ${name} 签发失败` })
      case 'cert_expiring':
        return a.severity === 'error'
          ? t('alerts.title.cert_expired', { name, defaultValue: `证书 ${name} 已过期` })
          : t('alerts.title.cert_expiring', { name, defaultValue: `证书 ${name} 即将到期` })
      case 'panel_upgrade':
        return t('alerts.title.panel_upgrade', { name, version: a.latest_version, defaultValue: `${name}：3X-UI ${a.latest_version} 可升级` })
      case 'psp_upgrade':
        return t('alerts.title.psp_upgrade', { version: a.latest_version, defaultValue: `面板新版本 ${a.latest_version} 可更新` })
      case 'login_security':
        return t('alerts.title.login_security', { count: a.count, defaultValue: `近期发生 ${a.count} 次登录锁定` })
      default:
        return name
    }
  }

  function alertSecondary(a: Alert): string {
    switch (a.type) {
      case 'node_health':
        return [a.panel_name, t(`nodes.health.${a.health_state}`, { defaultValue: a.health_state || '' })].filter(Boolean).join(' · ')
      case 'cert_failed':
        return a.last_error || ''
      case 'cert_expiring':
        return a.expire_at ? new Date(a.expire_at).toLocaleString() : ''
      case 'panel_upgrade':
      case 'psp_upgrade':
        return `${a.current_version || '?'} → ${a.latest_version || '?'}`
      default:
        return ''
    }
  }

  function openMenu(e: MouseEvent<HTMLElement>) {
    setAnchor(e.currentTarget)
    void refetch() // refresh on open
  }

  function go(a: Alert) {
    setAnchor(null)
    // PSP self-update has no in-app page — open the GitHub releases externally.
    if (a.type === 'psp_upgrade') {
      window.open(PSP_RELEASES_URL, '_blank', 'noopener,noreferrer')
      return
    }
    navigate(ROUTE[a.type] ?? '/admin/dashboard')
  }

  return (
    <>
      <Tooltip title={t('alerts.bell_title', { defaultValue: '通知' })}>
        <IconButton onClick={openMenu} aria-label="notifications" sx={{ color: md.onSurface }}>
          <Badge badgeContent={total} max={99} color={badgeColor} overlap="circular">
            <NotificationsNoneIcon />
          </Badge>
        </IconButton>
      </Tooltip>
      <Menu
        open={!!anchor}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: { mt: 1, width: 360, maxWidth: '90vw', maxHeight: 480 } } }}
      >
        <Box sx={{ px: 2, py: 1.25, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Typography sx={{ fontWeight: 600, color: md.onSurface }}>{t('alerts.bell_title', { defaultValue: '通知' })}</Typography>
          {isFetching && <CircularProgress size={14} />}
        </Box>
        <Divider />
        {total === 0 ? (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, px: 2, py: 3, color: md.onSurfaceVariant, fontSize: 14 }}>
            {/* "No notifications" is a claim about the server; a failed request
                is not evidence for it. Never present an outage as an empty feed. */}
            {isError ? (
              <>
                <ErrorOutlineIcon sx={{ fontSize: 18, color: md.error }} />
                {t('alerts.unavailable', { defaultValue: '通知暂时不可用' })}
              </>
            ) : (
              <>
                <CheckCircleIcon sx={{ fontSize: 18, color: '#22c55e' }} />
                {t('alerts.empty', { defaultValue: '暂无通知' })}
              </>
            )}
          </Box>
        ) : (
          alerts.map(a => (
            <MenuItem key={a.key} onClick={() => go(a)} sx={{ alignItems: 'flex-start', py: 1, whiteSpace: 'normal' }}>
              <ListItemIcon sx={{ color: severityColor(a.severity), mt: 0.25, minWidth: 34 }}>
                {typeIcon(a.type, a.severity)}
              </ListItemIcon>
              <Box sx={{ minWidth: 0 }}>
                <Typography sx={{ fontSize: 14, fontWeight: 500, color: md.onSurface }}>{alertTitle(a)}</Typography>
                {alertSecondary(a) && (
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, wordBreak: 'break-word' }}>
                    {alertSecondary(a)}
                  </Typography>
                )}
              </Box>
            </MenuItem>
          ))
        )}
      </Menu>
    </>
  )
}
