import type { TFunction } from 'i18next'
import type { Server } from '@/api/servers'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { IconDelete, IconEdit, IconExternalLink, IconTraffic } from '@/lib/icons'
import { maintenanceActions, type MaintenanceHandlers } from './maintenance'

export interface RowMenuHandlers {
  open: (s: Server) => void
  editSettings: (s: Server) => void
  metrics: (s: Server) => void
  remove: (s: Server) => void
  /** The upgrades (agent or panel, and the core), the same ones the Maintenance tab lists. */
  upgradeAgent: (s: Server) => void
  upgradePanel: (s: Server) => void
  core: (s: Server) => void
}

/**
 * The More menu of one server in the list: what is reached from a list all the time. Upgrading is (an operator works
 * down the list when a release lands), so the upgrade actions stay here with the same gating as the Maintenance tab;
 * installing, the credential, shared ports and route cleanup live on the server's page only. Host metrics stay because
 * they are the read an operator takes in passing. Destructive last, below the divider (MoreMenu does that).
 */
export function rowMenu(s: Server, h: RowMenuHandlers, o: { t: TFunction; busy: boolean }): MoreMenuItem[] {
  const { t } = o
  const items: MoreMenuItem[] = [
    { id: 'open', label: t('admin:servers.action.open'), icon: IconExternalLink, onSelect: () => h.open(s) },
    { id: 'edit', label: t('admin:servers.action.edit_settings'), icon: IconEdit, onSelect: () => h.editSettings(s) },
  ]
  if (s.panel_type === 'psp') items.push({ id: 'metrics', label: t('admin:nodeMetrics.title', { name: s.name }), icon: IconTraffic, onSelect: () => h.metrics(s) })
  const noop = () => {}
  const mh: MaintenanceHandlers = { install: noop, upgradeAgent: h.upgradeAgent, upgradePanel: h.upgradePanel, core: h.core, rotate: noop, sharedPorts: noop, retiredRoutes: noop }
  const upgrades = maintenanceActions(s, mh, { t, admin: false, busy: o.busy }).filter(a => a.group === 'upgrade')
  upgrades.forEach((a, i) => items.push({
    id: `upgrade-${a.id}`, label: a.label, icon: a.icon, disabled: a.disabled, disabledReason: a.disabledReason, onSelect: a.onSelect, separatorBefore: i === 0,
  }))
  items.push({ id: 'delete', label: t('admin:servers.action.delete'), icon: IconDelete, destructive: true, onSelect: () => h.remove(s) })
  return items
}
