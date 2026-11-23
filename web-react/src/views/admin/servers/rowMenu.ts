import type { TFunction } from 'i18next'
import type { Server } from '@/api/servers'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { IconClean, IconDelete, IconDiagnose, IconEdit, IconKey, IconPercent, IconTraffic, IconTune, IconUpgrade, IconDownload, IconSplit } from '@/lib/icons'
import { hasCapability, typeLabel } from './serverModel'

export interface RowMenuHandlers {
  edit: (s: Server) => void
  install: (s: Server) => void
  upgradeAgent: (s: Server) => void
  upgradePanel: (s: Server) => void
  core: (s: Server) => void
  rotate: (s: Server) => void
  lineFactors: (s: Server) => void
  sharedPorts: (s: Server) => void
  retiredRoutes: (s: Server) => void
  metrics: (s: Server) => void
  diagnostics: (s: Server) => void
  remove: (s: Server) => void
}

/**
 * The More menu of one server. AN ACTION THE SERVER CANNOT DO IS SHOWN DISABLED WITH ITS REASON IN THE LABEL, never
 * hidden and never offered and then refused by the backend: the capability list the backend sends per server decides
 * (S-UI has no remote upgrade, and the panel would answer 501). Actions that belong to one kind of server only (the
 * agent's, 3X-UI's route cleanup) appear for that kind; capability-gated ones appear for every kind they could apply to.
 * Destructive last, below the divider (MoreMenu does that).
 */
export function rowMenu(s: Server, h: RowMenuHandlers, o: { t: TFunction; admin: boolean; busy: boolean }): MoreMenuItem[] {
  const { t } = o
  const native = s.panel_type === 'psp'
  const off = (label: string, reason: string) => t('admin:servers.menu.unsupported', { action: label, reason })
  const items: MoreMenuItem[] = [{ id: 'edit', label: t('admin:servers.action.edit'), icon: IconEdit, onSelect: () => h.edit(s) }]

  if (o.admin) items.push({ id: 'install', label: t('admin:servers.install_reinstall.action'), icon: IconDownload, onSelect: () => h.install(s) })

  if (native) {
    const label = t('admin:servers.agent_upgrade.action')
    const ready = !!s.node_upgrade_ready
    items.push({ id: 'agent', label: ready ? label : off(label, t('admin:servers.menu.not_ready')), icon: IconUpgrade, disabled: !ready || o.busy, onSelect: () => h.upgradeAgent(s) })
  } else {
    const label = t('admin:servers.action.upgrade_panel')
    const can = hasCapability(s, 'panel.upgrade')
    items.push({ id: 'panel', label: can ? label : off(label, t('admin:servers.menu.not_supported', { type: typeLabel(s) })), icon: IconUpgrade, disabled: !can || o.busy, onSelect: () => h.upgradePanel(s) })
  }

  const coreLabel = native ? t('admin:servers.action.select_core') : t('admin:servers.action.upgrade_xray')
  const canCore = hasCapability(s, 'core.upgrade')
  items.push({ id: 'core', label: canCore ? coreLabel : off(coreLabel, t('admin:servers.menu.not_supported', { type: typeLabel(s) })), icon: native ? IconTune : IconUpgrade, disabled: !canCore || o.busy, onSelect: () => h.core(s) })

  if (native) items.push({ id: 'rotate', label: t('admin:servers.action.rotate_node_credential'), icon: IconKey, disabled: o.busy, onSelect: () => h.rotate(s) })
  items.push({ id: 'factors', label: t('admin:servers.action.line_factors'), icon: IconPercent, onSelect: () => h.lineFactors(s) })
  if (native) items.push({ id: 'ports', label: t('admin:servers.action.shared_ports'), icon: IconSplit, onSelect: () => h.sharedPorts(s) })
  if (s.panel_type === '3xui') items.push({ id: 'retired', label: t('admin:servers.retired_routes.open'), icon: IconClean, onSelect: () => h.retiredRoutes(s) })
  if (native) items.push({ id: 'metrics', label: t('admin:nodeMetrics.title', { name: s.name }), icon: IconTraffic, onSelect: () => h.metrics(s) })
  // ADMIN ONLY, mirroring the admin route group the endpoint lives in: the result is the machine's detail, and an
  // operator who can see the server list must not reach it. Gated on config.write, the SPA's admin-only capability.
  if (native && o.admin) items.push({ id: 'diagnostics', label: t('admin:nodeDiagnostics.title', { name: s.name }), icon: IconDiagnose, onSelect: () => h.diagnostics(s) })
  items.push({ id: 'delete', label: t('admin:servers.action.delete'), icon: IconDelete, destructive: true, onSelect: () => h.remove(s) })
  return items
}
