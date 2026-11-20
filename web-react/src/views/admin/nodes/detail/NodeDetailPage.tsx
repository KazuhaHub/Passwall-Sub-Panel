import { useMemo, useRef, useState } from 'react'
import { Link, Navigate } from 'react-router'
import type { OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import { PageHeader } from '@/components/common/PageHeader'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useTabParam } from '@/hooks/useTabParam'
import { IconDelete } from '@/lib/icons'
import { useAllServers, useNodeOrder } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { ExitTab } from '../exit/ExitTab'
import { GeneralTab } from '../general/GeneralTab'
import { InboundTab } from '../inbound/InboundTab'
import { useNodeDetail } from '../lines/useNodeDetail'
import { useNodeDeletion } from '../delete/useNodeDeletion'
import { useRowHandlers } from '../list/useRowHandlers'
import { useNodesT } from '../shared/useNodesT'
import DetailLinesTab from './LinesTab'
import { ListenerBanner } from './ListenerBanner'
import { useDirtyTabs } from './useDirtyTabs'

const TABS = ['lines', 'exit', 'inbound', 'general'] as const
type Tab = (typeof TABS)[number]
/** A listener has no lines and no name of its own to edit (design 4.7): what is left is its inbound and its routes. */
const LISTENER_TABS: readonly Tab[] = ['exit', 'inbound']
const NOOP = () => {}
const NO_LINE_HANDLERS = { edit: NOOP, add: NOOP, toggle: NOOP, remove: NOOP }

/**
 * The header's own actions on the node: enable or disable, and the server-side ones in the menu. Delete and
 * remove-from-panel open the same impact dialogs as the list (what the delete takes with it, read first), so the
 * two places cannot disagree. `deleting` says one of them was opened: when the node is gone afterwards the page
 * goes back to the list instead of saying "not found".
 */
function useHeaderActions(node: Node | undefined, deleting: { current: boolean }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const servers = useAllServers(scope)
  const order = useNodeOrder(scope)
  const serverMap = useMemo(() => new Map((servers.data ?? []).map(s => [s.id, s])), [servers.data])
  const deletion = useNodeDeletion({ rows: order.data?.rows ?? [], serverMap })
  const handlers = useRowHandlers({ lines: NO_LINE_HANDLERS, moveTo: NOOP, editSeparator: NOOP, deletion })
  if (!node) return { toggle: null, more: [] as MoreMenuItem[], dialog: deletion.element }
  const caps = serverMap.get(node.panel_id)?.capabilities ?? []
  // The row handlers take an order row; a node page has only the node, which is all they read (its id and names).
  const row = { kind: 'line', node_id: node.id, line_id: 'direct', name: node.display_name, landing: { node_id: node.id, name: node.display_name, enabled: node.enabled, panel_id: node.panel_id } } as unknown as OrderRow
  const more: MoreMenuItem[] = [
    { id: 'recreate', label: t('nodes.action.recreate_inbound'), disabled: !(caps.includes('inbound.create') && caps.includes('inbound.update') && caps.includes('inbound.delete')), onSelect: () => void handlers.recreateInbound(row) },
    { id: 'remove', label: t('nodes.action.remove_from_panel'), onSelect: () => { deleting.current = true; handlers.removeFromPanel(row) } },
    { id: 'delete', label: t('nodes.action.delete_node'), icon: IconDelete, destructive: true, disabled: !caps.includes('inbound.delete'), onSelect: () => { deleting.current = true; handlers.deleteNode(row) } },
  ]
  const toggle = { label: t(node.enabled ? 'nodes.action.disable_node' : 'nodes.action.enable_node'), disabled: !caps.includes('inbound.enable'), run: () => handlers.toggleNode(row, !node.enabled) }
  return { toggle, more, dialog: deletion.element }
}

/**
 * `/admin/nodes/:id` (design 4.2). The four tabs are the four questions about a node: how users get in (Lines),
 * where traffic leaves (Exit), how the listener is configured (Inbound), what it is called (General). Each edits
 * and saves its own part, and a tab is built on first visit and then kept, so a draft survives a switch of tabs
 * and a switch never asks anything. Leaving the PAGE asks, once, and names the tabs that have unsaved edits.
 */
export default function NodeDetailPage({ id }: { id: number }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const query = useNodeDetail(id)
  const order = useNodeOrder(scope)
  const node = query.data?.node
  const listener = (node?.parent_node_id ?? 0) > 0
  const [asked, setTab] = useTabParam<Tab>('tab', 'lines', TABS)
  const tab: Tab = listener && !LISTENER_TABS.includes(asked) ? 'inbound' : asked
  const [visited, setVisited] = useState<ReadonlySet<Tab>>(new Set([tab]))
  const visit = (next: Tab) => { setVisited(v => (v.has(next) ? v : new Set(v).add(next))); setTab(next) }
  const shown = visited.has(tab) ? visited : new Set(visited).add(tab)

  const { dirty, report } = useDirtyTabs(TABS)
  const names = dirty.map(d => t(`nodes.line.tabs.${d}`)).join(t('nodes.detail.tab_separator'))
  const copy = discardChangesConfirm(t)
  useLeaveGuard(dirty.length > 0, { ...copy, message: t('nodes.detail.dirty_tabs', { tabs: names }) }, (next, current) => next.pathname !== current.pathname)

  const published = useMemo(() => {
    const lines = (order.data?.rows ?? []).filter(r => r.kind === 'line' && r.node_id === id)
    return lines.length === 0 ? undefined : t('nodes.line.published_count', { published: lines.filter(r => r.published).length, total: lines.length })
  }, [order.data, id, t])
  const deleting = useRef(false)
  const { toggle, more, dialog } = useHeaderActions(listener ? undefined : node, deleting)

  const back = <Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>
  const gone = query.isError && (query.error as { response?: { status?: number } } | null)?.response?.status === 404
  // A refetch that fails keeps the node it had, so "deleted from here" is read from the error itself.
  if (gone && deleting.current) return <Navigate to="/admin/nodes" replace />
  if (query.isError && !node) return gone ? <EmptyState title={t('nodes.detail.not_found')} action={back} /> : <ErrorState reason={query.error} retry={() => void query.refetch()} />
  if (!node) return <LoadingState />

  const tabs = listener ? LISTENER_TABS : TABS
  const mounted = (name: Tab) => shown.has(name)
  return (
    <>
      <PageHeader
        title={node.display_name}
        description={[node.panel_name, node.server_address, published].filter(Boolean).join(' · ')}
        secondary={back}
        primary={toggle && <Button variant="outline" disabled={toggle.disabled} onClick={toggle.run}>{toggle.label}</Button>}
        more={more}
      />
      {listener && <div className="mb-4"><ListenerBanner node={node} /></div>}
      <Tabs value={tab} onValueChange={v => visit(v as Tab)}>
        <TabsList>{tabs.map(name => <TabsTrigger key={name} value={name}>{t(`nodes.line.tabs.${name}`)}</TabsTrigger>)}</TabsList>
        {/* keepMounted: the tabs build their own forms, which the Tabs component cannot see, so it would unmount them. */}
        {!listener && <TabsContent value="lines" keepMounted>{mounted('lines') && <DetailLinesTab nodeId={id} />}</TabsContent>}
        <TabsContent value="exit" keepMounted>{mounted('exit') && <ExitTab node={node} routesOnly={listener} onDirtyChange={report.exit} />}</TabsContent>
        <TabsContent value="inbound" keepMounted>{mounted('inbound') && <InboundTab node={node} onDirtyChange={report.inbound} />}</TabsContent>
        {!listener && <TabsContent value="general" keepMounted>{mounted('general') && <GeneralTab node={node} onDirtyChange={report.general} />}</TabsContent>}
      </Tabs>
      {dialog}
    </>
  )
}
