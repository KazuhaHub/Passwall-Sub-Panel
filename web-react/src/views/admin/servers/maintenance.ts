import type { TFunction } from 'i18next'
import type { Server } from '@/api/servers'
import type { IconType } from '@/lib/icons'
import { IconClean, IconDownload, IconKey, IconSplit, IconTune, IconUpgrade } from '@/lib/icons'
import { hasCapability, typeLabel } from './serverModel'

export interface MaintenanceHandlers {
  install: (s: Server) => void
  upgradeAgent: (s: Server) => void
  upgradePanel: (s: Server) => void
  core: (s: Server) => void
  rotate: (s: Server) => void
  sharedPorts: (s: Server) => void
  retiredRoutes: (s: Server) => void
}

export type MaintenanceGroup = 'install' | 'upgrade' | 'access'

export interface MaintenanceAction {
  id: string
  group: MaintenanceGroup
  label: string
  /** What it does, in a sentence, so the page can say it without a tooltip. */
  description: string
  icon: IconType
  disabled?: boolean
  /** Why it is off; shown under the label. An action a server cannot do is shown off with its reason, never hidden. */
  disabledReason?: string
  onSelect: () => void
}

/**
 * The actions on the machine behind one server, for its Maintenance tab. THE GATING IS THE LIST MENU'S FORMER RULE,
 * unchanged: AN ACTION THE SERVER CANNOT DO IS SHOWN DISABLED WITH ITS REASON, never hidden and never offered and then
 * refused by the backend: the capability list the backend sends per server decides (S-UI has no remote upgrade, and the
 * panel would answer 501). Actions that belong to one kind of server only (the agent's, 3X-UI's route cleanup) appear for
 * that kind; capability-gated ones appear for every kind they could apply to.
 */
export function maintenanceActions(s: Server, h: MaintenanceHandlers, o: { t: TFunction; admin: boolean; busy: boolean }): MaintenanceAction[] {
  const { t } = o
  const native = s.panel_type === 'psp'
  const out: MaintenanceAction[] = []

  if (o.admin) out.push({ id: 'install', group: 'install', label: t('admin:servers.install_reinstall.action'), description: t('admin:servers.maintenance.install'), icon: IconDownload, onSelect: () => h.install(s) })

  if (native) {
    const ready = !!s.node_upgrade_ready
    out.push({ id: 'agent', group: 'upgrade', label: t('admin:servers.agent_upgrade.action'), description: t('admin:servers.maintenance.agent'), icon: IconUpgrade,
      disabled: !ready || o.busy, disabledReason: ready ? undefined : t('admin:servers.menu.not_ready'), onSelect: () => h.upgradeAgent(s) })
  } else {
    const can = hasCapability(s, 'panel.upgrade')
    out.push({ id: 'panel', group: 'upgrade', label: t('admin:servers.action.upgrade_panel'), description: t('admin:servers.maintenance.panel'), icon: IconUpgrade,
      disabled: !can || o.busy, disabledReason: can ? undefined : t('admin:servers.menu.not_supported', { type: typeLabel(s) }), onSelect: () => h.upgradePanel(s) })
  }

  const canCore = hasCapability(s, 'core.upgrade')
  out.push({ id: 'core', group: 'upgrade', label: native ? t('admin:servers.action.select_core') : t('admin:servers.action.upgrade_xray'),
    description: t(native ? 'admin:servers.maintenance.core_native' : 'admin:servers.maintenance.core'), icon: native ? IconTune : IconUpgrade,
    disabled: !canCore || o.busy, disabledReason: canCore ? undefined : t('admin:servers.menu.not_supported', { type: typeLabel(s) }), onSelect: () => h.core(s) })

  if (native) {
    out.push({ id: 'rotate', group: 'access', label: t('admin:servers.action.rotate_node_credential'), description: t('admin:servers.maintenance.rotate'), icon: IconKey, disabled: o.busy, onSelect: () => h.rotate(s) })
    out.push({ id: 'ports', group: 'access', label: t('admin:servers.action.shared_ports'), description: t('admin:servers.maintenance.ports'), icon: IconSplit, onSelect: () => h.sharedPorts(s) })
  }
  if (s.panel_type === '3xui') out.push({ id: 'retired', group: 'access', label: t('admin:servers.retired_routes.open'), description: t('admin:servers.maintenance.retired'), icon: IconClean, onSelect: () => h.retiredRoutes(s) })
  return out
}
