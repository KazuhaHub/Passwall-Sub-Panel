import { Alert, Box, Button, Chip, CircularProgress, Table, TableBody, TableCell, TableHead, TableRow, Typography } from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { deleteNode } from '@/api/nodes'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { frontKeys } from '@/query/keys'
import { frontErrorOf, type PortBehind, type PortUsage } from '@/api/fronts'
import InfoTip from '@/components/InfoTip'
import { useServerPorts } from '@/query/fronts'
import { useQueryScope } from '@/query/useQueryScope'
import { listenerLabel } from './listenerLabel'

/** What a front's target is, in the admin's words. */
export function behindText(t: (key: string, values?: Record<string, unknown>) => string, b: PortBehind): string {
  const target = b.target
  switch (target.kind) {
    case 'listener': return b.parent_node_id
      ? t('admin:servers.fronts.ports.to_tunnel_listener', { label: listenerLabel(t, '', b), port: b.loopback_port ?? 0 })
      : t('admin:servers.fronts.ports.to_listener', { port: b.loopback_port ?? 0 })
    case 'external': return t('admin:servers.fronts.ports.to_external', { host: target.host ?? '', port: target.port ?? 0 })
    default: return b.label
  }
}

function what(t: (key: string, values?: Record<string, unknown>) => string, u: PortUsage): string {
  switch (u.kind) {
    case 'listener': return u.orphan ? u.label : u.parent_node_id
      ? t('admin:servers.fronts.ports.kind.tunnel_listener', { label: listenerLabel(t, '', u), protocol: u.protocol ?? '' })
      : t('admin:servers.fronts.ports.kind.listener', { name: u.node_name ?? '', protocol: u.protocol ?? '' })
    case 'forward': return t('admin:servers.fronts.ports.kind.forward', { label: u.label })
    case 'link_planned': return t('admin:servers.fronts.ports.kind.link_planned')
    case 'link_reserved': return t('admin:servers.fronts.ports.kind.link_reserved', { peer: u.peer_panel_id ?? 0 })
    case 'link': return t('admin:servers.fronts.ports.kind.link', { peer: u.peer_panel_id ?? 0 })
    case 'front': return t('admin:servers.fronts.ports.kind.front')
    case 'front_loopback': return u.parent_node_id
      ? t('admin:servers.fronts.ports.kind.tunnel_front_loopback', { label: listenerLabel(t, '', u) })
      : t('admin:servers.fronts.ports.kind.front_loopback', { name: u.node_name ?? '' })
    case 'probe_loopback': return u.peer_panel_id
      ? t('admin:servers.fronts.ports.kind.probe_loopback', { peer: u.peer_panel_id })
      : t('admin:servers.fronts.ports.kind.probe_loopback_other')
    case 'probe_range': return t('admin:servers.fronts.ports.kind.probe_range')
    case 'reserved': return t('admin:servers.fronts.ports.kind.reserved')
  }
}

/** Every port the server uses, read only. */
export default function PortUsageTable({ panelId }: { panelId: number }) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const ports = useServerPorts(scope, panelId)
  const queryClient = useQueryClient()

  // A hidden tunnel listener whose line is gone cannot be reached from any node list: remove it here.
  async function removeOrphan(u: PortUsage) {
    const ok = await confirm({
      title: t('admin:servers.fronts.ports.orphan_remove_title'),
      message: t('admin:servers.fronts.ports.orphan_remove_message', { name: u.node_name ?? `#${u.node_id}`, port: u.port }),
      destructive: true,
      confirmText: t('admin:servers.fronts.ports.orphan_remove'),
    })
    if (!ok || !u.node_id) return
    try {
      await deleteNode(u.node_id)
      pushSnack(t('admin:servers.fronts.ports.orphan_removed'), 'success')
      await queryClient.invalidateQueries({ queryKey: frontKeys.all(scope) })
    } catch (err) {
      // A refusal the request layer did not show (a confirmation that was declined) leaves everything as it was.
      if (!frontErrorOf(err)) throw err
    }
  }
  if (ports.isPending) {
    return <Box sx={{ display: 'flex', justifyContent: 'center', py: 3 }} role="status" aria-label={t('admin:servers.fronts.loading')}><CircularProgress size={24} /></Box>
  }
  if (ports.isError) return <Alert severity="error">{t('admin:servers.fronts.ports.load_failed')}</Alert>
  return (
    <Box>
      <Typography variant="body2" color="text.secondary" sx={{ display: 'flex', alignItems: 'center' }}>
        {t('admin:servers.fronts.ports.hint')}
        <InfoTip text={t('admin:servers.fronts.help.ports')} />
      </Typography>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>{t('admin:servers.fronts.ports.port')}</TableCell>
            <TableCell>{t('admin:servers.fronts.ports.bind')}</TableCell>
            <TableCell>{t('admin:servers.fronts.ports.used_by')}</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {ports.data.map((u, i) => (
            <TableRow key={`${u.kind}-${u.port}-${u.node_id ?? 0}-${u.line_id ?? ''}-${i}`}>
              <TableCell sx={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>
                {u.range_high ? `${u.port}–${u.range_high}` : u.port}
              </TableCell>
              <TableCell>{u.listen || t(`admin:servers.fronts.ports.bind_${u.bind}`)}</TableCell>
              <TableCell>
                <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
                  <span>{what(t, u)}</span>
                  {!u.enabled && <Chip size="small" label={t('admin:servers.fronts.ports.disabled')} />}
                  {u.inactive && <Chip size="small" color="warning" label={t('admin:servers.fronts.ports.inactive', { why: t(`admin:servers.fronts.withheld.reason.${u.inactive}`, { defaultValue: t(`admin:servers.fronts.unsupported.${u.inactive}`, { defaultValue: u.inactive }) }) })} />}
                  {u.orphan && <Chip size="small" color="warning" label={t('admin:servers.fronts.ports.orphan')} />}
                  {u.orphan && u.node_id !== undefined && (
                    <Button size="small" color="error" onClick={() => void removeOrphan(u)}>{t('admin:servers.fronts.ports.orphan_remove')}</Button>
                  )}
                  {u.unrouted && <Chip size="small" color="warning" label={t('admin:servers.fronts.ports.unrouted')} />}
                </Box>
                {u.behind?.map((b, j) => (
                  <Box key={j} sx={{ pl: 2, fontSize: 13 }}>
                    {b.is_default
                      ? t('admin:servers.fronts.ports.default_arrow', { to: behindText(t, b) })
                      : t('admin:servers.fronts.ports.route_arrow', { names: b.server_names.join(', '), to: behindText(t, b) })}
                    {!b.is_default && b.target.kind !== 'listener' && ` — ${b.label}`}
                  </Box>
                ))}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Box>
  )
}
