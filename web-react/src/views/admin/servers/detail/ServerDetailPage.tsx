import { useCallback, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { ObjectPage } from '@/components/common/ObjectPage'
import { ObjectTabs, type ObjectTab } from '@/components/common/ObjectTabs'
import { useBackTarget } from '@/components/common/objectReturn'
import { pushSnack } from '@/components/feedback/toasts'
import { Badge } from '@/components/ui/badge'
import { IconDelete } from '@/lib/icons'
import { ServerDeleteDialog } from '../../nodes/delete/ServerDeleteDialog'
import NodeDiagnosticsDialog from '../dialogs/NodeDiagnosticsDialog'
import NodeMetricsDialog from '../metrics/NodeMetricsDialog'
import { AddressSummary } from '../AddressList'
import { ServerSettingsForm } from '../page/ServerFormPage'
import { TestResult } from '../page/TestResult'
import { SERVERS_PATH } from '../serverRoutes'
import { splitVersionIdentity, typeLabel } from '../serverModel'
import { useReleaseInfo } from '../useReleaseInfo'
import { useServerProbes } from '../useServerProbes'
import { useServerCache } from '../useServerRecord'
import { ServerMaintenance, ServerMonitoring, ServerOverview } from './panels'
import { useServerMaintenance } from './useServerMaintenance'

type TabId = 'overview' | 'settings' | 'maintenance' | 'monitoring'

/**
 * `/admin/servers/:id`: one server as a page. Overview says what it is and whether it answers; Settings is the form
 * (unchanged: its draft, its guard on leaving, its Save); Maintenance holds what changes the machine; Monitoring (Passwall
 * Node only) the host reads. The tab is `?tab=`, so a tab is a link. The header keeps the connection test and the
 * delete, which belong to the server, not to a tab.
 */
export function ServerDetailPage({ server }: { server: Server }) {
  const { t } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const back = useBackTarget(SERVERS_PATH)
  const cache = useServerCache()
  const native = server.panel_type === 'psp'

  // The probe of this one server, on demand only: opening the page never reaches the upstream server.
  const probes = useServerProbes([], cache.patch)
  const probe = probes.stateFor(server)
  const releases = useReleaseInfo([server], String(server.id))
  const maintenance = useServerMaintenance(server, probes)

  const [deleting, setDeleting] = useState(false)
  const [metricsOpen, setMetricsOpen] = useState(false)
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false)
  const [settingsDirty, setSettingsDirty] = useState(false)
  // A delete done from this page makes the draft moot. A ref set before navigating, because `dirty` clears a render later.
  const leaving = useRef(false)
  // The form guards its own draft and reads this flag to let the delete through.
  const onDirty = useCallback((d: boolean) => setSettingsDirty(d), [])

  const tabs = useMemo<ObjectTab<TabId>[]>(() => [
    { id: 'overview', label: t('admin:servers.detail.tabs.overview') },
    { id: 'settings', label: t('admin:servers.detail.tabs.settings'), dirty: settingsDirty },
    { id: 'maintenance', label: t('admin:servers.detail.tabs.maintenance') },
    ...(native ? [{ id: 'monitoring' as const, label: t('admin:servers.detail.tabs.monitoring') }] : []),
  ], [t, settingsDirty, native])

  const identity = splitVersionIdentity(server.panel_version)
  const panels: Record<TabId, React.ReactNode> = {
    overview: <ServerOverview server={server} probe={probe} releases={releases} />,
    settings: <ServerSettingsForm editing={server} leaving={leaving} onDirtyChange={onDirty} />,
    maintenance: <ServerMaintenance actions={maintenance.actions} />,
    monitoring: <ServerMonitoring server={server} onMetrics={() => setMetricsOpen(true)} onDiagnostics={() => setDiagnosticsOpen(true)} />,
  }

  return (
    <ObjectPage
      back={{ to: back, label: t('admin:servers.title') }}
      title={server.name}
      badges={<Badge variant="outline">{typeLabel(server)}</Badge>}
      subtitle={native ? undefined : server.url}
      status={(
        <>
          <TestResult probe={probe} />
          {identity.display && <span className="text-sm text-muted-foreground">{identity.display}</span>}
          <div className="min-w-0 max-w-full sm:max-w-md"><AddressSummary s={server} /></div>
          <span className="text-xs text-muted-foreground">{t('admin:servers.form.test_hint')}</span>
        </>
      )}
      secondary={<AsyncButton variant="outline" disabled={probe.status === 'checking'} onClick={() => probes.runTest(server)}>{t('admin:servers.action.test')}</AsyncButton>}
      more={[{ id: 'delete', label: t('admin:servers.action.delete'), icon: IconDelete, destructive: true, onSelect: () => setDeleting(true) }]}
    >
      <ObjectTabs tabs={tabs} panels={panels} unsavedLabel={t('admin:servers.detail.unsaved')} />
      {maintenance.element}
      {native && <NodeMetricsDialog server={server} open={metricsOpen} onClose={() => setMetricsOpen(false)} />}
      {native && <NodeDiagnosticsDialog server={server} open={diagnosticsOpen} onClose={() => setDiagnosticsOpen(false)} />}
      {deleting && (
        <ServerDeleteDialog server={server} onClose={() => setDeleting(false)}
          onDeleted={() => {
            leaving.current = true
            setDeleting(false)
            pushSnack(t('admin:servers.toast.deleted'), 'success')
            cache.dropProbe(server.id)
            void cache.invalidate()
            void navigate(back, { replace: true })
          }} />
      )}
    </ObjectPage>
  )
}
