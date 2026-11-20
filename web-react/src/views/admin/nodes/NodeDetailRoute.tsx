import { useEffect, useRef } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { getNode } from '@/api/nodes'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/common/PageHeader'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { LegacyEditorsProvider, useLegacyEditors } from './legacy/LegacyEditors'
import { useNodesT } from './shared/useNodesT'

/**
 * `/admin/nodes/:id`. The detail page with its four tabs is N2 and N3; until then this is a shell that offers the
 * legacy editors, so the route, the back link and the `?tab=inbound` deep link already work. Admin only (D3).
 */
function Shell({ id }: { id: number }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const legacy = useLegacyEditors()
  const query = useQuery({ queryKey: [...nodeKeys.all(scope), 'detail', id], queryFn: ({ signal }) => getNode(id, { signal }) })
  const node = query.data?.node
  // `?tab=inbound` (from the access-control coverage sheet) opens the inbound editor once the node is known.
  const asked = useRef(false)
  const tab = params.get('tab')
  useEffect(() => {
    if (tab === 'inbound' && node && !asked.current) { asked.current = true; legacy.openInbound(node) }
  }, [tab, node, legacy])

  if (query.isError) return <ErrorState reason={query.error} retry={() => query.refetch()} />
  return (
    <>
      <PageHeader
        title={node?.display_name ?? ''}
        description={node ? `${node.panel_name} · ${node.server_address}` : undefined}
        secondary={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>}
        primary={node && <Button onClick={() => legacy.openEdit(node)}>{t('nodes.detail.edit_legacy')}</Button>}
      />
      {node && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => legacy.openInbound(node)}>{t('nodes.edit_inbound')}</Button>
          <Button variant="ghost" onClick={() => navigate(`/admin/nodes?q=${encodeURIComponent(node.display_name)}`)}>{t('nodes.detail.show_lines')}</Button>
        </div>
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
