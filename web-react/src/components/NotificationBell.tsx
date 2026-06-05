import { useCallback, useEffect, useRef, useState, type MouseEvent } from 'react'
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
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline'
import ScheduleIcon from '@mui/icons-material/Schedule'
import SystemUpdateAltIcon from '@mui/icons-material/SystemUpdateAlt'
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

import { getAlerts, type Alert, type AlertSeverity, type AlertType } from '@/api/alerts'

const ROUTE: Record<AlertType, string> = {
  node_health: '/admin/nodes',
  cert_failed: '/admin/certs',
  cert_expiring: '/admin/certs',
  panel_upgrade: '/admin/servers',
  user_expiring: '/admin/users',
  login_security: '/admin/logs',
}

const SEVERITY_RANK: Record<AlertSeverity, number> = { error: 0, warning: 1, info: 2 }

const POLL_MS = 60_000

function typeIcon(type: AlertType, severity: AlertSeverity) {
  switch (type) {
    case 'cert_failed':
      return <ErrorOutlineIcon fontSize="small" />
    case 'cert_expiring':
    case 'user_expiring':
      return <ScheduleIcon fontSize="small" />
    case 'panel_upgrade':
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

  const [alerts, setAlerts] = useState<Alert[]>([])
  const [loading, setLoading] = useState(true)
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await getAlerts()
      const sorted = [...res.alerts].sort((a, b) => SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity])
      setAlerts(sorted)
    } catch {
      // quiet — keep the previous snapshot on a transient failure
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    timer.current = setInterval(() => { void load() }, POLL_MS)
    return () => { if (timer.current) clearInterval(timer.current) }
  }, [load])

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
      case 'user_expiring':
        return t('alerts.title.user_expiring', { name, defaultValue: `用户 ${name} 即将到期` })
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
      case 'user_expiring':
        return a.expire_at ? new Date(a.expire_at).toLocaleString() : ''
      case 'panel_upgrade':
        return `${a.current_version || '?'} → ${a.latest_version || '?'}`
      default:
        return ''
    }
  }

  function openMenu(e: MouseEvent<HTMLElement>) {
    setAnchor(e.currentTarget)
    setLoading(true) // show the spinner during the on-open refresh
    void load() // refresh on open
  }

  function go(a: Alert) {
    setAnchor(null)
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
          {loading && <CircularProgress size={14} />}
        </Box>
        <Divider />
        {total === 0 ? (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, px: 2, py: 3, color: md.onSurfaceVariant, fontSize: 14 }}>
            <CheckCircleIcon sx={{ fontSize: 18, color: '#22c55e' }} />
            {t('alerts.empty', { defaultValue: '暂无通知' })}
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
