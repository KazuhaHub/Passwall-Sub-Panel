import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/servers'
import { useObjectLink } from '@/components/common/objectReturn'
import { useCan } from '@/utils/permissions'
import SharedPortsDialog from '../../fronts/SharedPortsDialog'
import { CoreDialog } from '../dialogs/CoreDialog'
import { NativeAgentUpgradeDialog } from '../dialogs/NativeAgentUpgradeDialog'
import { NodeMigrationPreviewDialog } from '../dialogs/NodeMigrationPreviewDialog'
import { ReinstallBackendDialog } from '../dialogs/ReinstallBackendDialog'
import RetiredRouteCleanupDialog from '../dialogs/RetiredRouteCleanupDialog'
import { leaveInstallation } from '../install/installHandoff'
import { rotateWithConfirm } from '../install/rotateCredential'
import { maintenanceActions, type MaintenanceAction } from '../maintenance'
import { serverInstallPath } from '../serverRoutes'
import { useServerCache } from '../useServerRecord'
import type { useServerProbes } from '../useServerProbes'
import { useServerBusy, useServerUpgrades } from '../useServerUpgrades'

/**
 * The maintenance of ONE server: the actions the Maintenance tab lists and the dialogs they open over the page. The
 * dialogs are the ones the list's menu used to open; only where they open moved. `element` is rendered once by the page.
 */
export function useServerMaintenance(server: Server, probes: ReturnType<typeof useServerProbes>): { actions: MaintenanceAction[]; element: React.ReactNode } {
  const { t } = useTranslation(['admin', 'common'])
  const admin = useCan('config.write')
  const cache = useServerCache()
  const navigate = useNavigate()
  const link = useObjectLink()
  const [, setParams] = useSearchParams()

  const [coreOpen, setCoreOpen] = useState(false)
  const [upgradeOpen, setUpgradeOpen] = useState(false)
  const [migrationOpen, setMigrationOpen] = useState(false)
  const [reinstallOpen, setReinstallOpen] = useState(false)
  const [sharedPortsOpen, setSharedPortsOpen] = useState(false)
  const [retiredOpen, setRetiredOpen] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [coreBusy, setCoreBusy] = useState(false)

  const up = useServerUpgrades({ probeServer: s => probes.probeServer(s), refresh: () => void cache.invalidate(), clearSelection: () => {} })

  // The installation page carries this page's place (its back link returns here). A rotation that finishes after the
  // administrator moved on must not pull them to the installation page.
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  function openInstallation(s: Server) { const l = link(serverInstallPath(s.id)); void navigate(l.to, { state: l.state }) }
  const openSettings = () => setParams(prev => { const next = new URLSearchParams(prev); next.set('tab', 'settings'); return next }, { replace: true })

  async function rotate(s: Server) {
    setRotating(true)
    try {
      const provisioned = await rotateWithConfirm(t, s)
      // The credential is shown once: it travels in memory to the installation page, never in the address or history.
      if (provisioned && mounted.current) {
        leaveInstallation({ server: provisioned.server, provisioning: provisioned, creating: false })
        openInstallation(provisioned.server)
      }
    } finally { if (mounted.current) setRotating(false) }
  }

  const upgradingElsewhere = useServerBusy(server.id)
  const busy = up.upgrading === server.id || upgradingElsewhere || rotating || coreBusy
  const actions = maintenanceActions(server, {
    install: () => setReinstallOpen(true), upgradeAgent: () => setUpgradeOpen(true), upgradePanel: s => void up.runUpgradePanel(s),
    core: () => setCoreOpen(true), rotate: s => void rotate(s), sharedPorts: () => setSharedPortsOpen(true), retiredRoutes: () => setRetiredOpen(true),
  }, { t, admin, busy })

  const element = (
    <>
      <NativeAgentUpgradeDialog server={upgradeOpen ? server : null} onClose={() => { setUpgradeOpen(false); void cache.invalidate() }} />
      {coreOpen && (
        <CoreDialog key={server.id} server={server} onClose={() => setCoreOpen(false)}
          onSelected={(s, engine, version) => cache.patch(prev => prev.map(i => (i.id === s.id ? { ...i, desired_core_engine: engine, desired_core_version: version } : i)))}
          onUpgraded={s => void probes.probeServer(s)} onBusy={id => setCoreBusy(id !== null)} />
      )}
      {retiredOpen && <RetiredRouteCleanupDialog server={server} onClose={() => setRetiredOpen(false)} />}
      {sharedPortsOpen && <SharedPortsDialog key={`sharedports-${server.id}`} server={server} onClose={() => setSharedPortsOpen(false)} />}
      <ReinstallBackendDialog key={`reinstall-${reinstallOpen ? server.id : 'closed'}`} server={reinstallOpen ? server : null}
        onClose={() => setReinstallOpen(false)}
        onNativeInstall={s => { setReinstallOpen(false); openInstallation(s) }}
        onNativeMigration={() => { setReinstallOpen(false); setMigrationOpen(true) }}
        onConfigure={() => { setReinstallOpen(false); openSettings() }} />
      <NodeMigrationPreviewDialog key={migrationOpen ? server.id : 'closed'} server={migrationOpen ? server : null}
        onRefreshServer={() => void cache.invalidate()} onClose={() => { setMigrationOpen(false); void cache.invalidate() }}
        onNativeInstall={s => { setMigrationOpen(false); openInstallation(s) }} />
    </>
  )
  return { actions, element }
}
