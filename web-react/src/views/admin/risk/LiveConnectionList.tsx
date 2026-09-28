import { useState } from 'react'
import {
  Box, Chip, IconButton, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Tooltip, Typography, useTheme,
} from '@mui/material'
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown'
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp'
import PersonSearchOutlinedIcon from '@mui/icons-material/PersonSearchOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

import type { ConnDevice, ConnRegion, LiveConnection, LiveUser } from '@/api/riskCenter'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import { exclusionLabelKey, judgementColor, userLabel } from '@/utils/riskCenter'

// Expand button, user, connections, notes.
const COLS = 4

export interface LiveConnectionListProps {
  /** One page of accounts, as the live view returns them. */
  users: LiveUser[]
  /** The window devices were inferred over, for the hint beside them. */
  deviceWindowHours: number
  /** The fetches could not be read: devices are absent, not "none". */
  devicesUnavailable?: boolean
  onOpenUser?: (userId: number) => void
  /** Every account starts expanded (the single-account lookup). */
  initiallyOpen?: boolean
}

/**
 * The live connections, one row per account, each expanding to a table of its
 * connections: address and source, panel and node, place, judgement, the
 * panel's last sighting and the devices inferred behind it.
 *
 * Shared by the Live tab and the user lookup, so the two cannot describe the
 * same connection differently.
 */
export default function LiveConnectionList({
  users, deviceWindowHours, devicesUnavailable = false, onOpenUser, initiallyOpen = false,
}: LiveConnectionListProps) {
  const { t } = useTranslation(['admin'])
  // Toggled ids: the ones that differ from the initial state. A new page of
  // accounts then opens (or stays closed) as the list says, without an
  // effect resetting anything.
  const [toggled, setToggled] = useState<ReadonlySet<number>>(() => new Set())
  const isOpen = (id: number) => initiallyOpen !== toggled.has(id)
  const toggle = (id: number) => setToggled(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  return (
    <TableContainer>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell sx={{ width: 48 }} />
            <TableCell>{t('admin:risk_center.live.col_user')}</TableCell>
            <TableCell align="right">{t('admin:risk_center.live.col_connections')}</TableCell>
            <TableCell />
          </TableRow>
        </TableHead>
        <TableBody>
          {users.map(u => (
            <UserRow key={u.user_id} user={u} open={isOpen(u.user_id)} onToggle={() => toggle(u.user_id)}
              onOpenUser={onOpenUser} deviceWindowHours={deviceWindowHours} devicesUnavailable={devicesUnavailable} />
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  )
}

function UserRow({ user, open, onToggle, onOpenUser, deviceWindowHours, devicesUnavailable }: {
  user: LiveUser
  open: boolean
  onToggle: () => void
  onOpenUser?: (userId: number) => void
  deviceWindowHours: number
  devicesUnavailable: boolean
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <>
      <TableRow hover>
        <TableCell>
          <IconButton size="small" onClick={onToggle}
            aria-label={open ? t('admin:risk_center.live.hide_connections') : t('admin:risk_center.live.show_connections')}>
            {open ? <KeyboardArrowUpIcon fontSize="small" /> : <KeyboardArrowDownIcon fontSize="small" />}
          </IconButton>
        </TableCell>
        <TableCell>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <span>{userLabel(user)}</span>
            {user.display_name && user.upn && (
              <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{user.display_name}</Typography>
            )}
            {onOpenUser && (
              <Tooltip title={t('admin:risk_center.open_user')}>
                <IconButton size="small" onClick={() => onOpenUser(user.user_id)}>
                  <PersonSearchOutlinedIcon fontSize="inherit" />
                </IconButton>
              </Tooltip>
            )}
          </Box>
        </TableCell>
        <TableCell align="right">
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
            <span>{user.connections.length}</span>
            {/* A list taken while a panel holding this account was unread is
                a FLOOR; shown as a plain count it would read as the whole. */}
            {user.unread_panels > 0 && (
              <Tooltip title={t('admin:risk_center.live.incomplete')}>
                <WarningAmberIcon fontSize="inherit" color="warning" />
              </Tooltip>
            )}
          </Box>
        </TableCell>
        <TableCell sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {user.stale_addresses > 0 && t('admin:risk_center.live.stale_addresses', { count: user.stale_addresses })}
        </TableCell>
      </TableRow>
      {open && (
        <TableRow>
          <TableCell colSpan={COLS} sx={{ bgcolor: md.surfaceContainerLow, py: 1 }}>
            <ConnectionTable conns={user.connections} deviceWindowHours={deviceWindowHours}
              devicesUnavailable={devicesUnavailable} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

function ConnectionTable({ conns, deviceWindowHours, devicesUnavailable }: {
  conns: LiveConnection[]
  deviceWindowHours: number
  devicesUnavailable: boolean
}) {
  const { t } = useTranslation(['admin'])
  const panelTz = useSiteStore(s => s.timezone)
  return (
    <Table size="small">
      <TableHead>
        <TableRow>
          <TableCell>{t('admin:risk_center.live.col_ip')}</TableCell>
          <TableCell>{t('admin:risk_center.live.col_panel')}</TableCell>
          <TableCell>{t('admin:risk_center.live.col_region')}</TableCell>
          <TableCell>{t('admin:risk_center.live.col_status')}</TableCell>
          <TableCell>{t('admin:risk_center.live.col_seen')}</TableCell>
          <TableCell>{t('admin:risk_center.live.col_device')}</TableCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {conns.map(c => (
          <TableRow key={`${c.panel_id}/${c.node}/${c.source_key}`}>
            <TableCell><AddressCell ip={c.ip} sourceKey={c.source_key} /></TableCell>
            <TableCell><PanelCell name={c.panel_name} id={c.panel_id} node={c.node} /></TableCell>
            <TableCell sx={{ fontSize: 12 }}>{regionText(c.region)}</TableCell>
            <TableCell><JudgementChip exclusion={c.exclusion} /></TableCell>
            <TableCell sx={{ fontSize: 12, whiteSpace: 'nowrap' }}>
              {/* The PANEL's clock, said so: a skewed node clock would
                  otherwise read as a connection in the future or the past.
                  Printed in the panel's timezone like every other time. */}
              {c.seen_at > 0
                ? t('admin:risk_center.live.seen_panel_clock', { time: formatMsDualTz(c.seen_at * 1000, panelTz) })
                : t('admin:risk_center.live.seen_none')}
            </TableCell>
            <TableCell>
              <DeviceCell conn={c} hours={deviceWindowHours} unavailable={devicesUnavailable} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

/** A region as the sub-log list prints one; '—' when nothing placed it. */
export function regionText(r: ConnRegion | null): string {
  return (r && formatRegion(r)) || '—'
}

/**
 * The address in monospace, and under it the SOURCE the detector counts when
 * that differs (an IPv6 /64): the address is for recognition, the source is
 * what was judged.
 */
export function AddressCell({ ip, sourceKey }: { ip: string; sourceKey: string }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <>
      <Box sx={{ fontFamily: 'monospace', fontSize: 13, wordBreak: 'break-all' }}>{ip || sourceKey}</Box>
      {ip && sourceKey && sourceKey !== ip && (
        <Box sx={{ fontSize: 11, color: md.onSurfaceVariant, wordBreak: 'break-all' }}>
          {t('admin:risk_center.live.source_key', { key: sourceKey })}
        </Box>
      )}
    </>
  )
}

/** The panel by name (by id once deleted), and the raw 3X-UI node id under
 *  it: PSP has no name for a node, and a guessed one would mislead. */
export function PanelCell({ name, id, node }: { name: string; id: number; node: string }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <>
      <Box sx={{ fontSize: 13 }}>{name || `#${id}`}</Box>
      {node && (
        <Tooltip title={t('admin:risk_center.live.node_hint')}>
          <Box sx={{ fontFamily: 'monospace', fontSize: 11, color: md.onSurfaceVariant, wordBreak: 'break-all' }}>{node}</Box>
        </Tooltip>
      )}
    </>
  )
}

/** Judged, or the reason it was set aside. Never in the success colour
 *  (judgementColor); a reason this build does not know prints raw. */
export function JudgementChip({ exclusion }: { exclusion: string }) {
  const { t } = useTranslation(['admin'])
  const key = exclusionLabelKey(exclusion)
  const label = exclusion === '' ? t('admin:risk_center.live.kept') : key ? t(key) : exclusion
  return (
    <Chip size="small" color={judgementColor(exclusion)} variant={exclusion === '' ? 'filled' : 'outlined'}
      label={label} />
  )
}

function DeviceCell({ conn, hours, unavailable }: { conn: LiveConnection; hours: number; unavailable: boolean }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const none = (text: string) => <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{text}</Typography>
  // Through PSP's own relay every account's fetches share one address:
  // nothing about the device behind it can be told, and "not inferable"
  // alone would hide why.
  if (conn.exclusion === 'infra') return none(t('admin:risk_center.live.device_via_relay'))
  if (unavailable) return none('—')
  if (conn.devices.length === 0) return none(t('admin:risk_center.live.device_none'))
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
      {conn.devices.map((d, i) => <DeviceLine key={`${d.device_id4}/${d.ua}/${i}`} device={d} hours={hours} />)}
    </Box>
  )
}

/** One inferred device, labelled "inferred" every time: the connection
 *  carried no device, and a device shown bare would read as a fact. */
function DeviceLine({ device, hours }: { device: ConnDevice; hours: number }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const name = device.label || (device.device_id4 ? `#${device.device_id4}` : device.client_type || device.ua || '—')
  const detail = [
    t('admin:risk_center.live.device_hint', { hours }),
    t('admin:risk_center.live.device_fetches', {
      count: device.fetches, time: formatMsDualTz(device.last_at_ms, panelTz),
    }),
    device.ua,
  ].filter(Boolean).join('\n')
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
      <Tooltip title={<Box sx={{ whiteSpace: 'pre-line' }}>{detail}</Box>}>
        <Chip size="small" variant="outlined" label={name} />
      </Tooltip>
      {device.label && device.device_id4 && (
        <Box component="span" sx={{ fontFamily: 'monospace', fontSize: 11, color: md.onSurfaceVariant }}>{`#${device.device_id4}`}</Box>
      )}
      <Chip size="small" variant="outlined" color="info" label={t('admin:risk_center.live.device_inferred')} />
    </Box>
  )
}
