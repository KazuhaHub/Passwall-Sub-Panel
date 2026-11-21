import { useMemo } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { useQueryClient } from '@tanstack/react-query'
import { deleteNode } from '@/api/nodes'
import { frontErrorOf, type PortBehind, type PortUsage } from '@/api/fronts'
import { StatusBadge } from '@/components/common/StatusBadge'
import { InfoTip } from '@/components/common/HelpTip'
import { DataTable } from '@/components/data/DataTable'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { AsyncButton } from '@/components/common/AsyncButton'
import { frontKeys } from '@/query/keys'
import { useServerPorts } from '@/query/fronts'
import { useQueryScope } from '@/query/useQueryScope'
import { useAdminT } from '../useAdminT'
import { listenerLabel } from './listenerLabel'

type Translate = (key: string, values?: Record<string, unknown>) => string

/** What a front's target is, in the admin's words. */
export function behindText(t: Translate, b: PortBehind): string {
  const target = b.target
  switch (target.kind) {
    case 'listener': return b.parent_node_id
      ? t('admin:servers.fronts.ports.to_tunnel_listener', { label: listenerLabel(t, '', b), port: b.loopback_port ?? 0 })
      : t('admin:servers.fronts.ports.to_listener', { port: b.loopback_port ?? 0 })
    case 'external': return t('admin:servers.fronts.ports.to_external', { host: target.host ?? '', port: target.port ?? 0 })
    default: return b.label
  }
}

function what(t: Translate, u: PortUsage): string {
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

const rowKey = (u: PortUsage, i: number) => `${u.kind}-${u.port}-${u.node_id ?? 0}-${u.line_id ?? ''}-${i}`

/** Every port the server uses, read only, except that a hidden tunnel listener whose line is gone can be removed here. */
export default function PortUsageTable({ panelId }: { panelId: number }) {
  const t = useAdminT()
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
      initialFocus: 'cancel',
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

  // Rows carry their position: the same port can appear for several uses, and nothing else tells them apart.
  const rows = useMemo(() => (ports.data ?? []).map((u, i) => ({ u, key: rowKey(u, i) })), [ports.data])
  type Row = (typeof rows)[number]

  const columns = useMemo<ColumnDef<Row>[]>(() => [
    {
      id: 'port', header: t('admin:servers.fronts.ports.port'), accessorFn: r => r.u.port, meta: { label: t('admin:servers.fronts.ports.port'), hideable: false, className: 'tabular-nums whitespace-nowrap font-medium' },
      cell: c => (c.row.original.u.range_high ? `${c.row.original.u.port}–${c.row.original.u.range_high}` : c.row.original.u.port),
    },
    {
      id: 'bind', header: t('admin:servers.fronts.ports.bind'), enableSorting: false, meta: { label: t('admin:servers.fronts.ports.bind'), className: 'whitespace-nowrap' },
      cell: c => c.row.original.u.listen || t(`admin:servers.fronts.ports.bind_${c.row.original.u.bind}`),
    },
    {
      id: 'used_by', header: t('admin:servers.fronts.ports.used_by'), enableSorting: false, meta: { label: t('admin:servers.fronts.ports.used_by'), hideable: false },
      cell: c => {
        const u = c.row.original.u
        return (
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span>{what(t, u)}</span>
              {!u.enabled && <StatusBadge tone="neutral" quiet label={t('admin:servers.fronts.ports.disabled')} />}
              {u.inactive && <StatusBadge tone="warning" label={t('admin:servers.fronts.ports.inactive', { why: t(`admin:servers.fronts.withheld.reason.${u.inactive}`, { defaultValue: t(`admin:servers.fronts.unsupported.${u.inactive}`, { defaultValue: u.inactive }) }) })} />}
              {u.orphan && <StatusBadge tone="warning" label={t('admin:servers.fronts.ports.orphan')} />}
              {u.unrouted && <StatusBadge tone="warning" label={t('admin:servers.fronts.ports.unrouted')} />}
              {u.orphan && u.node_id !== undefined && (
                <AsyncButton variant="outline" size="sm" onClick={() => removeOrphan(u)}>{t('admin:servers.fronts.ports.orphan_remove')}</AsyncButton>
              )}
            </div>
            {u.behind?.map((b, j) => (
              <span key={j} className="pl-3 text-[13px] text-muted-foreground">
                {b.is_default
                  ? t('admin:servers.fronts.ports.default_arrow', { to: behindText(t, b) })
                  : t('admin:servers.fronts.ports.route_arrow', { names: b.server_names.join(', '), to: behindText(t, b) })}
                {!b.is_default && b.target.kind !== 'listener' && ` — ${b.label}`}
              </span>
            ))}
          </div>
        )
      },
    },
  // removeOrphan closes over the query client and scope only; it is stable for the life of this table.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ], [t])

  return (
    <div className="flex flex-col gap-2">
      <p className="flex items-center text-sm text-muted-foreground">
        {t('admin:servers.fronts.ports.hint')}
        <InfoTip text={t('admin:servers.fronts.help.ports')} />
      </p>
      <DataTable<Row>
        data={rows} columns={columns} getRowId={r => r.key} pagination={false} columnMenu={false} maxHeight={null}
        loading={ports.isPending} error={ports.isError ? (ports.error as Error) : null} onRetry={() => void ports.refetch()}
        aria-label={t('admin:servers.fronts.tab_ports')}
      />
    </div>
  )
}
