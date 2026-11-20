import { useEffect, useMemo, useRef } from 'react'
import { Link, useParams } from 'react-router'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PageHeader } from '@/components/common/PageHeader'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { useTabParam } from '@/hooks/useTabParam'
import { useNodeOrder } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import DetailLinesTab from './detail/LinesTab'
import { LegacyEditorsProvider, useLegacyEditors } from './legacy/LegacyEditors'
import { useNodeDetail } from './lines/useNodeDetail'
import { useNodesT } from './shared/useNodesT'

const TABS = ['lines', 'exit', 'inbound', 'general'] as const
type Tab = (typeof TABS)[number]

/**
 * `/admin/nodes/:id`. The Lines tab is N2's. Exit, Inbound and General are N3's; until then they hand over to the
 * legacy editors, so the route, the tabs, the back link and the `?tab=inbound` deep link already work. Admin only (D3).
 */
function Shell({ id }: { id: number }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const [tab, setTab] = useTabParam<Tab>('tab', 'lines', TABS)
  const legacy = useLegacyEditors()
  const query = useNodeDetail(id)
  const order = useNodeOrder(scope)
  const node = query.data?.node
  const listener = (node?.parent_node_id ?? 0) > 0
  // `?tab=inbound` (from the access-control coverage sheet) opens the inbound editor once the node is known.
  const asked = useRef(false)
  useEffect(() => {
    if (tab === 'inbound' && node && !asked.current) { asked.current = true; legacy.openInbound(node) }
  }, [tab, node, legacy])
  const published = useMemo(() => {
    const lines = (order.data?.rows ?? []).filter(r => r.kind === 'line' && r.node_id === id)
    return lines.length === 0 ? undefined : t('nodes.line.published_count', { published: lines.filter(r => r.published).length, total: lines.length })
  }, [order.data, id, t])

  if (query.isError && !node) return <ErrorState reason={query.error} retry={() => void query.refetch()} />

  const handOff = (
    <div className="flex flex-wrap gap-2">
      {node && <Button variant="outline" onClick={() => legacy.openEdit(node)}>{t('nodes.detail.edit_legacy')}</Button>}
      {node && <Button variant="outline" onClick={() => legacy.openInbound(node)}>{t('nodes.edit_inbound')}</Button>}
    </div>
  )

  return (
    <>
      <PageHeader
        title={node?.display_name ?? ''}
        description={node ? [node.panel_name, node.server_address, published].filter(Boolean).join(' · ') : undefined}
        secondary={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>}
      />
      {listener ? (
        <div className="flex flex-col gap-3">
          <Alert><AlertDescription data-testid="listener-banner">{t('nodes.tunnel.listener_banner_no_line', { parent: node?.parent_node_name ?? `#${node?.parent_node_id}` })}</AlertDescription></Alert>
          {handOff}
        </div>
      ) : (
        <Tabs value={tab} onValueChange={v => setTab(v as Tab)}>
          <TabsList>
            <TabsTrigger value="lines">{t('nodes.line.tabs.lines')}</TabsTrigger>
            <TabsTrigger value="exit">{t('nodes.line.tabs.exit')}</TabsTrigger>
            <TabsTrigger value="inbound">{t('nodes.line.tabs.inbound')}</TabsTrigger>
            <TabsTrigger value="general">{t('nodes.line.tabs.general')}</TabsTrigger>
          </TabsList>
          <TabsContent value="lines">{node && <DetailLinesTab nodeId={id} />}</TabsContent>
          <TabsContent value="exit">{handOff}</TabsContent>
          <TabsContent value="inbound">{handOff}</TabsContent>
          <TabsContent value="general">{handOff}</TabsContent>
        </Tabs>
      )}
    </>
  )
}

export default function NodeDetailRoute() {
  const t = useNodesT()
  const admin = useCan('config.write')
  const { id } = useParams()
  const n = Number(id)
  if (!admin) return <EmptyState title={t('nodes.detail.admin_only')} action={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>} />
  if (!Number.isSafeInteger(n) || n <= 0) return <EmptyState title={t('nodes.detail.not_found')} action={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>} />
  return <LegacyEditorsProvider><Shell id={n} /></LegacyEditorsProvider>
}
