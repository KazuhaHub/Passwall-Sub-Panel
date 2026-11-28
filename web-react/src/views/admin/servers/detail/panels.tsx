import type { ReactNode } from 'react'
import { isAxiosError } from 'axios'
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/servers'
import { useObjectLink } from '@/components/common/objectReturn'
import { Button } from '@/components/ui/button'
import { useAllNodes } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { AddressGroups } from '../AddressList'
import { InterfaceList } from '../metrics/InterfaceList'
import { useNodeMetricsCurrent } from '@/query/nodeMetrics'
import { serverAddresses } from '../addresses'
import { CountsCell, StatusCell, VersionCell } from '../cells'
import type { MaintenanceAction, MaintenanceGroup } from '../maintenance'
import { typeLabel } from '../serverModel'
import type { ProbeState } from '../useServerProbes'
import type { useReleaseInfo } from '../useReleaseInfo'
import { IconDiagnose, IconTraffic } from '@/lib/icons'

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 py-3 sm:grid sm:grid-cols-[10rem_1fr] sm:gap-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  )
}

/**
 * What one server is, at a glance: whether it answers, which version and whether there is a newer one, the addresses it
 * has, and how many nodes and lines it carries (with the way to them). Every value is the list's own cell, so the page
 * and the list can never say two things about one server.
 */
export function ServerOverview({ server, probe, releases }: { server: Server; probe: ProbeState; releases: ReturnType<typeof useReleaseInfo> }) {
  const { t } = useTranslation('admin')
  const link = useObjectLink()
  const scope = useQueryScope()
  const nodes = useAllNodes(scope)
  // A read that fails shows a dash: it must never claim zero.
  const counts = nodes.data
    ? nodes.data.filter(n => n.panel_id === server.id).reduce((c, n) => ({ nodes: c.nodes + 1, lines: c.lines + (n.hide_direct ? 0 : 1) + (n.relays?.length ?? 0) }), { nodes: 0, lines: 0 })
    : undefined
  const addresses = serverAddresses(server)
  return (
    <dl className="divide-y">
      <Field label={t('admin:servers.table.status')}>
        <StatusCell s={server} probe={probe} access={undefined} onOpenAccess={() => {}} />
      </Field>
      <Field label={t('admin:servers.table.version')}>
        <VersionCell s={server} nodeReleases={releases.nodeReleases} nodeReleaseCheckFailed={releases.nodeReleaseCheckFailed} suiReleaseVersion={releases.suiReleaseVersion} />
      </Field>
      <Field label={t('admin:servers.addresses.title')}>
        {addresses.reported ? <AddressGroups addresses={addresses} />
          : server.address ? <span className="font-mono text-[0.8125rem]">{server.address}</span>
            : server.panel_type === 'psp' ? <span className="text-muted-foreground">{t('admin:servers.addresses.none')}</span>
              : <span className="font-mono text-[0.8125rem] break-all">{server.url}</span>}
      </Field>
      {server.panel_type === 'psp' && (
        <Field label={t('admin:servers.interfaces.title')}><ServerInterfaces server={server} /></Field>
      )}
      <Field label={t('admin:servers.table.counts')}>
        <div className="flex flex-wrap items-center gap-3">
          <CountsCell counts={counts} />
          <Button variant="outline" size="sm" render={<Link {...link(`/admin/nodes?q=${encodeURIComponent(server.name)}`)} />}>{t('admin:servers.detail.view_nodes')}</Button>
        </div>
      </Field>
      <Field label={t('admin:servers.table.type')}>{typeLabel(server)}</Field>
    </dl>
  )
}

/**
 * A Passwall Node's interfaces from its latest host sample. A server whose node cannot answer (not enrolled yet, no
 * host telemetry, or a panel without the endpoint: all 404) says the node has not reported them rather than claiming a
 * load failure; a stale sample says so above the list.
 */
function ServerInterfaces({ server }: { server: Server }) {
  const { t } = useTranslation('admin')
  const scope = useQueryScope()
  const current = useNodeMetricsCurrent(scope, server.id)
  if (current.isPending) return <span className="text-muted-foreground">…</span>
  if (current.isError) {
    const missing = isAxiosError(current.error) && current.error.response?.status === 404
    return <span className="text-muted-foreground">{t(missing ? 'admin:servers.interfaces.unreported' : 'admin:servers.interfaces.load_failed')}</span>
  }
  const data = current.data
  if (!data.network) {
    return <span className="text-muted-foreground">{t(data.available ? 'admin:nodeMetrics.waitingForSample' : 'admin:servers.interfaces.unreported')}</span>
  }
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {data.freshness === 'stale' && <p className="text-xs text-muted-foreground">{t('admin:nodeMetrics.staleHint')}</p>}
      <InterfaceList network={data.network} />
    </div>
  )
}

const GROUPS: MaintenanceGroup[] = ['install', 'upgrade', 'access']

/**
 * The actions on the machine, grouped by what they touch. An action the server cannot do stays here, off, with its
 * reason in words under it (the old menu showed that only as a tooltip).
 */
export function ServerMaintenance({ actions }: { actions: MaintenanceAction[] }) {
  const { t } = useTranslation('admin')
  return (
    <div className="flex flex-col gap-6">
      {GROUPS.map(group => {
        const rows = actions.filter(a => a.group === group)
        if (rows.length === 0) return null
        return (
          <section key={group} aria-labelledby={`maint-${group}`} className="flex flex-col gap-2">
            <h2 id={`maint-${group}`} className="text-sm font-medium">{t(`admin:servers.maintenance.group.${group}`)}</h2>
            <ul className="divide-y rounded-lg border">
              {rows.map(a => (
                <li key={a.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-3 py-3">
                  <div className="min-w-0 flex-1 basis-60">
                    <p className="text-sm font-medium">{a.label}</p>
                    <p className="text-xs text-muted-foreground">{a.description}</p>
                    {a.disabledReason && <p className="mt-1 text-xs text-muted-foreground">{a.disabledReason}</p>}
                  </div>
                  <Button variant="outline" size="sm" aria-label={a.label} aria-disabled={a.disabled || undefined} onClick={() => { if (!a.disabled) a.onSelect() }}>
                    <a.icon aria-hidden />{t('admin:servers.maintenance.run')}
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

/** The reads on a Passwall Node's host: its metrics for everyone who can see the server, its diagnostics for the administrator only. */
export function ServerMonitoring({ server, onMetrics, onDiagnostics }: { server: Server; onMetrics: () => void; onDiagnostics: () => void }) {
  const { t } = useTranslation('admin')
  // ADMIN ONLY, mirroring the admin route group the endpoint lives in: the result is the machine's detail, and an
  // operator who can see the server must not reach it. Gated on config.write, the SPA's admin-only capability.
  const admin = useCan('config.write')
  const row = (title: string, text: string, onClick: () => void, icon: ReactNode) => (
    <li className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-3 py-3">
      <div className="min-w-0 flex-1 basis-60">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-xs text-muted-foreground">{text}</p>
      </div>
      <Button variant="outline" size="sm" aria-label={title} onClick={onClick}>{icon}{t('admin:servers.monitoring.open')}</Button>
    </li>
  )
  return (
    <ul className="divide-y rounded-lg border">
      {row(t('admin:nodeMetrics.title', { name: server.name }), t('admin:servers.monitoring.metrics'), onMetrics, <IconTraffic aria-hidden />)}
      {admin && row(t('admin:nodeDiagnostics.title', { name: server.name }), t('admin:servers.monitoring.diagnostics'), onDiagnostics, <IconDiagnose aria-hidden />)}
    </ul>
  )
}
