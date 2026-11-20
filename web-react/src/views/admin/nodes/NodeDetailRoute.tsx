import { Link, useParams } from 'react-router'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/feedback/EmptyState'
import { useCan } from '@/utils/permissions'
import NodeDetailPage from './detail/NodeDetailPage'
import { useNodesT } from './shared/useNodesT'

/** `/admin/nodes/:id`: administrators only (D3: the page reads endpoints operators cannot), and only for a real id. */
export default function NodeDetailRoute() {
  const t = useNodesT()
  const admin = useCan('config.write')
  const { id } = useParams()
  const n = Number(id)
  const back = <Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>
  if (!admin) return <EmptyState title={t('nodes.detail.admin_only')} action={back} />
  if (!Number.isSafeInteger(n) || n <= 0) return <EmptyState title={t('nodes.detail.not_found')} action={back} />
  return <NodeDetailPage id={n} />
}
