import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/feedback/EmptyState'
import { useCan } from '@/utils/permissions'
import CreatePage from './create/CreatePage'
import { useNodesT } from './shared/useNodesT'

/** `/admin/nodes/new`: administrators only. */
export default function NodeCreateRoute() {
  const t = useNodesT()
  const admin = useCan('config.write')
  if (!admin) return <EmptyState title={t('nodes.detail.admin_only')} action={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>} />
  return <CreatePage />
}
