import type { TFunction } from 'i18next'
import type { Server } from '@/api/servers'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { IconDelete, IconEdit, IconExternalLink, IconTraffic } from '@/lib/icons'

export interface RowMenuHandlers {
  open: (s: Server) => void
  editSettings: (s: Server) => void
  metrics: (s: Server) => void
  remove: (s: Server) => void
}

/**
 * The More menu of one server in the list: only what is reached from a list all the time. Everything that changes the
 * machine (installing, upgrading, the core, the credential, shared ports, route cleanup) and its diagnostics live on the
 * server's own page, where an action can say why it is off and what it does; the menu opens that page. Host metrics stay
 * because they are the read an operator takes in passing. Destructive last, below the divider (MoreMenu does that).
 */
export function rowMenu(s: Server, h: RowMenuHandlers, o: { t: TFunction }): MoreMenuItem[] {
  const { t } = o
  const items: MoreMenuItem[] = [
    { id: 'open', label: t('admin:servers.action.open'), icon: IconExternalLink, onSelect: () => h.open(s) },
    { id: 'edit', label: t('admin:servers.action.edit_settings'), icon: IconEdit, onSelect: () => h.editSettings(s) },
  ]
  if (s.panel_type === 'psp') items.push({ id: 'metrics', label: t('admin:nodeMetrics.title', { name: s.name }), icon: IconTraffic, onSelect: () => h.metrics(s) })
  items.push({ id: 'delete', label: t('admin:servers.action.delete'), icon: IconDelete, destructive: true, onSelect: () => h.remove(s) })
  return items
}
