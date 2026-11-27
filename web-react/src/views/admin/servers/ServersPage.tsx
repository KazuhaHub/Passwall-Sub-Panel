import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import type { SortingState } from '@tanstack/react-table'
import type { DestNodeStatus } from '@/api/accessControl'
import type { Server, ServerListParams } from '@/api/servers'
import { PageHeader } from '@/components/common/PageHeader'
import { useObjectLink } from '@/components/common/objectReturn'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { useTableViewState } from '@/hooks/useTableViewState'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { useDestStatus } from '@/query/accessControl'
import { serverKeys } from '@/query/keys'
import { useAllNodes } from '@/query/nodes'
import { type ServerListResponse, useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import SharedPortsDialog from '../fronts/SharedPortsDialog'
import { ServerDeleteDialog } from '../nodes/delete/ServerDeleteDialog'
import { legacyPageSize } from '../logs/shared'
import { CountsColumn, StatusColumn, VersionColumn, ActionsColumn, serverColumns } from './columns'
import { CoreDialog } from './dialogs/CoreDialog'
import { LineMultipliersDialog } from './dialogs/LineMultipliersDialog'
import { NativeAgentUpgradeDialog } from './dialogs/NativeAgentUpgradeDialog'
import NodeDiagnosticsDialog from './dialogs/NodeDiagnosticsDialog'
import { NodeMigrationPreviewDialog } from './dialogs/NodeMigrationPreviewDialog'
import { ReinstallBackendDialog } from './dialogs/ReinstallBackendDialog'
import RetiredRouteCleanupDialog from './dialogs/RetiredRouteCleanupDialog'
import ServerAccessDialog from './dialogs/ServerAccessDialog'
import NodeMetricsDialog from './metrics/NodeMetricsDialog'
import { leaveInstallation } from './install/installHandoff'
import { rotateWithConfirm } from './install/rotateCredential'
import { ServersContext, type ServersCtx } from './serversContext'
import { hasCapability } from './serverModel'
import { NameCell, TypeCell } from './cells'
import { rowMenu } from './rowMenu'
import { NEW_SERVER_PATH, serverInstallPath, serverPath } from './serverRoutes'
import { ServersBanners } from './ServersBanners'
import { useReleaseInfo } from './useReleaseInfo'
import { useServerProbes } from './useServerProbes'
import { useServerUpgrades } from './useServerUpgrades'

const NONE: Server[] = []
const DEFAULT_SORT = 'id-asc'

/** `name-desc` in the address bar (the shape it always had) <-> the table's sorting state. */
function parseSort(raw: string): { by: string; dir: 'asc' | 'desc' } {
  const [by, dir] = raw.split('-')
  return { by: by || 'id', dir: dir === 'desc' ? 'desc' : 'asc' }
}

/**
 * The upstream servers PSP manages (3X-UI, S-UI, Passwall Node). One row per server: is it reachable, is its version
 * supported, does anything need attention, how many nodes and lines it carries. One visible action (test the
 * connection) and everything else in the More menu. A server (add / edit) and a Passwall Node's installation are pages
 * of their own, reached from here and returning here; the short tasks (metrics, diagnostics, shared ports, core) are
 * dialogs over the list.
 *
 * The list is a plain database read, never polled, and the connection probe is a separate concern: refreshing the list
 * must never imply a request to an upstream server (see useServerProbes).
 */
export default function ServersPage() {
  const { t } = useTranslation(['admin', 'common'])
  const admin = useCan('config.write')
  const canAccess = useCan('access.view')
  const scope = useQueryScope()
  const queryClient = useQueryClient()

  const [url, setUrl] = useUrlStates({ page: urlCodecs.int(1, { min: 1 }), q: urlCodecs.string(), sort: urlCodecs.string(DEFAULT_SORT) })
  const view = useTableViewState('admin.servers', { pageSize: legacyPageSize() })
  const { by: sortBy, dir: sortDir } = parseSort(url.sort)
  const pageSize = view.state.pageSize

  // Substring search lives on the backend (name, url, remark, username; case-insensitive).
  const listParams = useMemo<ServerListParams>(() => ({
    page: url.page, page_size: pageSize, keyword: url.q || undefined, sort_by: sortBy || undefined, sort_dir: sortDir,
  }), [url.page, pageSize, url.q, sortBy, sortDir])
  const serversQuery = useServersList(scope, listParams)
  const items = serversQuery.data?.items ?? NONE
  const total = serversQuery.data?.total ?? 0

  function mutateItems(updater: (prev: Server[]) => Server[]) {
    queryClient.setQueryData<ServerListResponse>(serverKeys.list(scope, listParams), prev => (prev ? { ...prev, items: updater(prev.items) } : prev))
  }
  const probes = useServerProbes(items, mutateItems)
  const releases = useReleaseInfo(items, probes.pageIdsKey)
  const refresh = () => { void serversQuery.refetch() }
  const load = () => { refresh(); releases.recheck() }

  // The access-control state of each native node. A failed or unwired read shows nothing at all: the list must not carry
  // noise for a feature the build does not have.
  const destStatus = useDestStatus(scope, { enabled: canAccess })
  const accessOf = (s: Server): DestNodeStatus | undefined => {
    const st = destStatus.data
    if (!canAccess || destStatus.isError || !st || s.panel_type !== 'psp' || st.generation <= 0) return undefined
    return st.nodes.find(n => n.panel_id === s.id)
  }
  // Node and line counts per server. A read that fails leaves the column as dashes: it must never claim zero.
  const nodes = useAllNodes(scope)
  const counts = useMemo(() => {
    if (!nodes.data) return undefined
    const m = new Map<number, { nodes: number; lines: number }>()
    for (const n of nodes.data) {
      const c = m.get(n.panel_id) ?? { nodes: 0, lines: 0 }
      c.nodes += 1
      c.lines += (n.hide_direct ? 0 : 1) + (n.relays?.length ?? 0)
      m.set(n.panel_id, c)
    }
    return m
  }, [nodes.data])

  // ---- selection (per page: it never carries a batch action onto rows nobody can see) ----
  const [selection, setSelection] = useState<Record<string, boolean>>({})
  useEffect(() => { setSelection({}) }, [probes.pageIdsKey])
  const up = useServerUpgrades({ probeServer: s => probes.probeServer(s), refresh, clearSelection: () => setSelection({}) })

  // ---- what is open over the list ----
  const [deleteTarget, setDeleteTarget] = useState<Server | null>(null)
  const [coreTarget, setCoreTarget] = useState<Server | null>(null)
  const [metricsTarget, setMetricsTarget] = useState<Server | null>(null)
  const [diagnosticsTarget, setDiagnosticsTarget] = useState<Server | null>(null)
  const [upgradeTarget, setUpgradeTarget] = useState<Server | null>(null)
  const [migrationTarget, setMigrationTarget] = useState<Server | null>(null)
  const [reinstallTarget, setReinstallTarget] = useState<Server | null>(null)
  const [lineFactorsTarget, setLineFactorsTarget] = useState<Server | null>(null)
  const [sharedPortsTarget, setSharedPortsTarget] = useState<Server | null>(null)
  const [retiredTarget, setRetiredTarget] = useState<Server | null>(null)
  const [accessNode, setAccessNode] = useState<{ node: DestNodeStatus; name: string } | null>(null)
  const [rotatingId, setRotatingId] = useState<number | null>(null)
  const [coreBusyId, setCoreBusyId] = useState<number | null>(null)
  // A server's page and its installation page are pages of their own: they carry this list's place (page, search, order),
  // so their back link returns to the list the way it was left.
  const link = useObjectLink()
  const navigate = useNavigate()
  const openEdit = (s: Server) => void navigate(...pageArgs(serverPath(s.id)))
  const openInstallation = (s: Server) => void navigate(...pageArgs(serverInstallPath(s.id)))
  function pageArgs(to: string): [string, { state: unknown }] { const l = link(to); return [l.to, { state: l.state }] }
  // A rotation that finishes after the administrator moved on must not pull them to the installation page.
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])

  async function rotateCredential(s: Server) {
    setRotatingId(s.id)
    try {
      const provisioned = await rotateWithConfirm(t, s)
      // The credential is shown once: it travels in memory to the installation page, never in the address or history.
      if (provisioned && mounted.current) {
        leaveInstallation({ server: provisioned.server, provisioning: provisioned, creating: false })
        void navigate(...pageArgs(serverInstallPath(provisioned.server.id)))
      }
    } finally { setRotatingId(null) }
  }

  const busyId = up.upgrading ?? rotatingId ?? coreBusyId
  const menuOf = (s: Server) => rowMenu(s, {
    edit: openEdit, install: setReinstallTarget, upgradeAgent: setUpgradeTarget, upgradePanel: x => void up.runUpgradePanel(x),
    core: setCoreTarget, rotate: x => void rotateCredential(x), lineFactors: setLineFactorsTarget, sharedPorts: setSharedPortsTarget,
    retiredRoutes: setRetiredTarget, metrics: setMetricsTarget, diagnostics: setDiagnosticsTarget, remove: setDeleteTarget,
  }, { t, admin, busy: busyId === s.id })

  const ctx: ServersCtx = {
    probeOf: probes.stateFor, accessOf, countsOf: (s: Server) => counts?.get(s.id) ?? (counts ? { nodes: 0, lines: 0 } : undefined),
    nodeReleases: releases.nodeReleases, nodeReleaseCheckFailed: releases.nodeReleaseCheckFailed, suiReleaseVersion: releases.suiReleaseVersion,
    testing: probes.testing, onTest: probes.runTest, onOpenAccess: (s: Server, node: DestNodeStatus) => setAccessNode({ node, name: s.name }), menuOf,
  }
  // DataTable memoises its rows on the columns' identity and renders a column's cell as a component, so the columns must
  // be ONE stable array; what the cells show comes from the context (see serversContext).
  const columns = useMemo(() => serverColumns(t), [t])

  const sorting: SortingState = [{ id: sortBy, desc: sortDir === 'desc' }]
  const filtered = url.q !== ''
  const unreadable = serversQuery.isError && !serversQuery.data

  return (
    <ServersContext.Provider value={ctx}>
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:servers.title')}
        count={serversQuery.data ? t('admin:servers.count', { count: total }) : undefined}
        description={t('admin:servers.subtitle')}
        secondary={(
          // "Test all" runs the connectivity probe across every server on the page without a selection: a quick sanity
          // check after a network change or an upgrade.
          <Button variant="outline" disabled={probes.testingAll || items.length === 0} aria-busy={probes.testingAll || undefined} onClick={() => void probes.runTestMany(items)}>
            {t('admin:servers.test_all')}
          </Button>
        )}
        primary={admin && <Button render={<Link {...link(NEW_SERVER_PATH)} />}>{t('admin:servers.create')}</Button>}
        more={[{ id: 'refresh', label: t('admin:servers.action.refresh'), onSelect: load }]}
      />
      <ServersBanners items={items} compat={releases.compatStatus} />
      <DataTable<Server>
        tableId="admin.servers"
        aria-label={t('admin:servers.title')}
        mode="server"
        data={items}
        total={total}
        columns={columns}
        getRowId={s => String(s.id)}
        loading={serversQuery.isPending}
        error={unreadable ? serversQuery.error ?? true : null}
        onRetry={refresh}
        page={url.page}
        onPageChange={(page, reason) => setUrl({ page }, reason === 'correction' ? { history: 'replace' } : undefined)}
        pageSize={pageSize}
        onPageSizeChange={n => { view.setPageSize(n); setUrl({ page: 1 }) }}
        sorting={sorting}
        onSortingChange={next => { const s = next[0]; setUrl({ sort: s ? `${s.id}-${s.desc ? 'desc' : 'asc'}` : DEFAULT_SORT, page: 1 }) }}
        selectable
        selection={selection}
        onSelectionChange={setSelection}
        rowLabel={s => s.name}
        onRowOpen={openEdit}
        maxHeight={null}
        renderCard={(s, { open }) => <ServerCard s={s} open={open} />}
        toolbar={(
          <FilterBar
            aria-label={t('admin:servers.title')}
            search={{ value: url.q, onChange: q => setUrl({ q, page: 1 }), placeholder: t('admin:servers.search_placeholder') }}
            onClearAll={filtered ? () => setUrl({ q: '', page: 1 }) : undefined}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:servers.no_match')} action={<Button variant="outline" size="sm" onClick={() => setUrl({ q: '', page: 1 })}>{t('admin:servers.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:servers.empty.title')} description={t('admin:servers.empty.description')}
              action={admin ? <Button render={<Link {...link(NEW_SERVER_PATH)} />}>{t('admin:servers.create')}</Button> : undefined} />}
        bulkActions={ctxBulk => {
          const rows = ctxBulk.loadedRows
          const core = rows.filter(s => s.panel_type !== 'psp' && hasCapability(s, 'core.upgrade'))
          const panel = rows.filter(s => hasCapability(s, 'panel.upgrade'))
          const busy = up.batchBusy !== '' || probes.testingAll
          return (
            <>
              <Button variant="outline" size="sm" disabled={busy} onClick={() => void probes.runTestMany(rows)}>{t('admin:servers.batch_test')}</Button>
              <Button variant="outline" size="sm" disabled={busy || core.length === 0} onClick={() => void up.batchUpgradeXray(core)}>{t('admin:servers.batch_upgrade_xray')}</Button>
              <Button variant="outline" size="sm" disabled={busy || panel.length === 0} onClick={() => void up.batchUpgradePanel(panel)}>{t('admin:servers.batch_upgrade_panel')}</Button>
              <Button variant="destructive" size="sm" disabled={busy} onClick={() => void up.batchDelete(rows)}>{t('admin:servers.batch_delete')}</Button>
            </>
          )
        }}
      />

      <NodeMetricsDialog server={metricsTarget} open={metricsTarget !== null} onClose={() => setMetricsTarget(null)} />
      <NodeDiagnosticsDialog server={diagnosticsTarget} open={diagnosticsTarget !== null} onClose={() => setDiagnosticsTarget(null)} />
      {accessNode && <ServerAccessDialog node={accessNode.node} serverName={accessNode.name} onClose={() => setAccessNode(null)} />}
      <NativeAgentUpgradeDialog server={upgradeTarget} onClose={() => { setUpgradeTarget(null); refresh() }} />
      {coreTarget && (
        <CoreDialog key={coreTarget.id} server={coreTarget} onClose={() => setCoreTarget(null)}
          onSelected={(s, engine, version) => mutateItems(prev => prev.map(i => (i.id === s.id ? { ...i, desired_core_engine: engine, desired_core_version: version } : i)))}
          onUpgraded={s => void probes.probeServer(s)} onBusy={setCoreBusyId} />
      )}
      {deleteTarget && (
        <ServerDeleteDialog server={deleteTarget} onClose={() => setDeleteTarget(null)}
          onDeleted={() => { setDeleteTarget(null); pushSnack(t('admin:servers.toast.deleted'), 'success'); load() }} />
      )}
      {retiredTarget && <RetiredRouteCleanupDialog server={retiredTarget} onClose={() => setRetiredTarget(null)} />}
      {sharedPortsTarget && <SharedPortsDialog key={`sharedports-${sharedPortsTarget.id}`} server={sharedPortsTarget} onClose={() => setSharedPortsTarget(null)} />}
      {lineFactorsTarget && <LineMultipliersDialog key={`linefactors-${lineFactorsTarget.id}`} server={lineFactorsTarget} onClose={() => setLineFactorsTarget(null)} />}
      <ReinstallBackendDialog key={`reinstall-${reinstallTarget?.id ?? 'closed'}`} server={reinstallTarget}
        onClose={() => setReinstallTarget(null)}
        onNativeInstall={s => { setReinstallTarget(null); openInstallation(s) }}
        onNativeMigration={s => { setReinstallTarget(null); setMigrationTarget(s) }}
        onConfigure={s => { setReinstallTarget(null); openEdit(s) }} />
      <NodeMigrationPreviewDialog key={migrationTarget?.id ?? 'closed'}
        server={migrationTarget ? items.find(i => i.id === migrationTarget.id) ?? migrationTarget : null}
        onRefreshServer={load} onClose={() => { setMigrationTarget(null); load() }}
        onNativeInstall={s => { setMigrationTarget(null); openInstallation(s) }} />
    </div>
    </ServersContext.Provider>
  )
}

/** A server at phone width: name and type, the state, the version, and the same menu. The card opens the server's page. */
function ServerCard({ s, open }: { s: Server; open?: () => void }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-start justify-between gap-2">
        <button type="button" onClick={open} className="min-w-0 flex-1 text-left outline-none focus-visible:underline"><NameCell s={s} /></button>
        <ActionsColumn s={s} />
      </div>
      <div className="flex flex-wrap items-start gap-x-6 gap-y-2">
        <StatusColumn s={s} />
        <VersionColumn s={s} />
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><TypeCell s={s} /> · <CountsColumn s={s} /></div>
      </div>
    </div>
  )
}
