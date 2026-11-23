import { useState } from 'react'

import type { ConnDevice, ConnRegion, LiveConnection, LiveUser } from '@/api/riskCenter'
import { IconButton } from '@/components/common/IconButton'
import { InfoTip } from '@/components/common/HelpTip'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { IconChevronDown, IconChevronUp, IconSearch, IconWarning } from '@/lib/icons'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import { exclusionLabelKey, userLabel } from '@/utils/riskCenter'
import { useRiskT } from './useRiskT'

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
  /** Every account starts expanded, for a list meant to be read whole. */
  initiallyOpen?: boolean
}

/**
 * The live connections, one row per account, each expanding to a table of its connections: address and
 * source, server and node, place, judgement, the scan that still saw it connected, and the devices
 * inferred behind it. A table of its own rather than DataTable because every row carries a second,
 * differently-shaped table under it.
 *
 * The Live tab's list. The drawer's 连接 tab draws the same connections with the same pieces
 * (AddressCell, JudgementChip, regionText), so the two cannot describe one connection differently.
 */
export default function LiveConnectionList({
  users, deviceWindowHours, devicesUnavailable = false, onOpenUser, initiallyOpen = false,
}: LiveConnectionListProps) {
  const { t } = useRiskT()
  // Toggled ids: the ones that differ from the initial state. A new page of accounts then opens (or
  // stays closed) as the list says, without an effect resetting anything.
  const [toggled, setToggled] = useState<ReadonlySet<number>>(() => new Set())
  const isOpen = (id: number) => initiallyOpen !== toggled.has(id)
  const toggle = (id: number) => setToggled(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })

  return (
    <div className="overflow-x-auto rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10"><span className="sr-only">{t('admin:risk_center.live.show_connections')}</span></TableHead>
            <TableHead>{t('admin:risk_center.live.col_user')}</TableHead>
            <TableHead className="text-right">{t('admin:risk_center.live.col_connections')}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {users.map(u => (
            <UserRow key={u.user_id} user={u} open={isOpen(u.user_id)} onToggle={() => toggle(u.user_id)}
              onOpenUser={onOpenUser} deviceWindowHours={deviceWindowHours} devicesUnavailable={devicesUnavailable} />
          ))}
        </TableBody>
      </Table>
    </div>
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
  const { t } = useRiskT()
  return (
    <>
      <TableRow>
        <TableCell>
          <IconButton size="icon-xs" aria-expanded={open}
            label={open ? t('admin:risk_center.live.hide_connections') : t('admin:risk_center.live.show_connections')} onClick={onToggle}>
            {open ? <IconChevronUp /> : <IconChevronDown />}
          </IconButton>
        </TableCell>
        <TableCell>
          <div className="flex items-center gap-1.5">
            <span>{userLabel(user)}</span>
            {user.display_name && user.upn && <span className="text-xs text-muted-foreground">{user.display_name}</span>}
            {onOpenUser && (
              <IconButton label={t('admin:risk_center.open_user')} size="icon-xs" onClick={() => onOpenUser(user.user_id)}>
                <IconSearch />
              </IconButton>
            )}
          </div>
        </TableCell>
        <TableCell className="text-right tabular-nums">
          <span className="inline-flex items-center gap-1">
            <span>{user.connections.length}</span>
            {/* A list taken while a server holding this account was unread is a FLOOR; shown as a plain
                count it would read as the whole. */}
            {user.unread_panels > 0 && (
              <Tooltip>
                <TooltipTrigger render={<span role="img" aria-label={t('admin:risk_center.live.incomplete')} className="text-warning-ink" />}>
                  <IconWarning className="size-3.5" />
                </TooltipTrigger>
                <TooltipContent>{t('admin:risk_center.live.incomplete')}</TooltipContent>
              </Tooltip>
            )}
          </span>
        </TableCell>
        <TableCell className="text-xs text-muted-foreground">
          {user.stale_addresses > 0 && t('admin:risk_center.live.stale_addresses', { count: user.stale_addresses })}
        </TableCell>
      </TableRow>
      {open && (
        <TableRow className="bg-muted/30 hover:bg-muted/30">
          <TableCell colSpan={COLS}>
            <ConnectionTable conns={user.connections} deviceWindowHours={deviceWindowHours} devicesUnavailable={devicesUnavailable} />
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
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('admin:risk_center.live.col_ip')}</TableHead>
          <TableHead>{t('admin:risk_center.live.col_panel')}</TableHead>
          <TableHead>{t('admin:risk_center.live.col_region')}</TableHead>
          <TableHead>{t('admin:risk_center.live.col_status')}</TableHead>
          <TableHead>
            <span className="inline-flex items-center gap-0.5 whitespace-nowrap">
              {t('admin:risk_center.live.col_seen')}
              <InfoTip text={t('admin:risk_center.live.still_connected_hint')} />
            </span>
          </TableHead>
          <TableHead>{t('admin:risk_center.live.col_device')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {conns.map(c => (
          <TableRow key={`${c.panel_id}/${c.node}/${c.source_key}`}>
            <TableCell><AddressCell ip={c.ip} sourceKey={c.source_key} /></TableCell>
            <TableCell><PanelCell name={c.panel_name} id={c.panel_id} node={c.node} /></TableCell>
            <TableCell className="text-xs">{regionText(c.region)}</TableCell>
            <TableCell><JudgementChip exclusion={c.exclusion} /></TableCell>
            <TableCell className="text-xs whitespace-nowrap">
              {/* Not when this address was last used. Xray keeps an address while any connection from it
                  is open, and 3X-UI's scan stamps each one it still lists with the scan's own time, on
                  the server's clock — one time for all of an account's addresses. Printed in the panel's
                  timezone like every other time. */}
              {c.seen_at > 0
                ? t('admin:risk_center.live.still_connected', { time: formatMsDualTz(c.seen_at * 1000, panelTz) })
                : t('admin:risk_center.live.seen_none')}
            </TableCell>
            <TableCell><DeviceCell conn={c} hours={deviceWindowHours} unavailable={devicesUnavailable} /></TableCell>
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
  const { t } = useRiskT()
  return (
    <>
      <div className="font-mono text-[13px] break-all">{ip || sourceKey}</div>
      {ip && sourceKey && sourceKey !== ip && (
        <div className="text-[11px] break-all text-muted-foreground">{t('admin:risk_center.live.source_key', { key: sourceKey })}</div>
      )}
    </>
  )
}

/** The server by name (by id once deleted), and the raw 3X-UI node id under it: PSP has no name for a
 *  node, and a guessed one would mislead. */
export function PanelCell({ name, id, node }: { name: string; id: number; node: string }) {
  const { t } = useRiskT()
  return (
    <>
      <div className="text-[13px]">{name || `#${id}`}</div>
      {node && (
        <Tooltip>
          <TooltipTrigger render={<div className="font-mono text-[11px] break-all text-muted-foreground" />}>{node}</TooltipTrigger>
          <TooltipContent>{t('admin:risk_center.live.node_hint')}</TooltipContent>
        </Tooltip>
      )}
    </>
  )
}

/** Judged, or the reason it was set aside. Never drawn as a good state: an excluded source was not judged
 *  at all, and a judged one is only counted, not cleared; a reason this build does not know prints raw. */
export function JudgementChip({ exclusion }: { exclusion: string }) {
  const { t } = useRiskT()
  const key = exclusionLabelKey(exclusion)
  const label = exclusion === '' ? t('admin:risk_center.live.kept') : key ? t(key) : exclusion
  return <Badge variant={exclusion === '' ? 'secondary' : 'outline'}>{label}</Badge>
}

function DeviceCell({ conn, hours, unavailable }: { conn: LiveConnection; hours: number; unavailable: boolean }) {
  const { t } = useRiskT()
  const none = (text: string) => <span className="text-xs text-muted-foreground">{text}</span>
  // Through PSP's own relay every account's fetches share one address: nothing about the device behind
  // it can be told, and "not inferable" alone would hide why.
  if (conn.exclusion === 'infra') return none(t('admin:risk_center.live.device_via_relay'))
  if (unavailable) return none('—')
  if (conn.devices.length === 0) return none(t('admin:risk_center.live.device_none'))
  return (
    <div className="flex flex-col gap-1">
      {conn.devices.map((d, i) => <DeviceLine key={`${d.device_id4}/${d.ua}/${i}`} device={d} hours={hours} />)}
    </div>
  )
}

/** One inferred device, labelled "inferred" every time: the connection carried no device, and a device
 *  shown bare would read as a fact. */
function DeviceLine({ device, hours }: { device: ConnDevice; hours: number }) {
  const { t } = useRiskT()
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
    <div className="flex flex-wrap items-center gap-1">
      <Tooltip>
        <TooltipTrigger render={<Badge variant="outline" />}>{name}</TooltipTrigger>
        <TooltipContent className="whitespace-pre-line">{detail}</TooltipContent>
      </Tooltip>
      {device.label && device.device_id4 && <span className="font-mono text-[11px] text-muted-foreground">{`#${device.device_id4}`}</span>}
      <Badge variant="secondary">{t('admin:risk_center.live.device_inferred')}</Badge>
    </div>
  )
}
