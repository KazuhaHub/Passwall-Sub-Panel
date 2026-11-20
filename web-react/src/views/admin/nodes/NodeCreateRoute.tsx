import { useEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/feedback/EmptyState'
import { useAllServers } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { LegacyEditorsProvider, useLegacyEditors } from './legacy/LegacyEditors'
import { useNodesT } from './shared/useNodesT'

/**
 * `/admin/nodes/new`. The create page with its four steps is N3; until then it mounts the legacy wizard and
 * returns to the list when the wizard closes. Admin only.
 */
function Wizard() {
  const t = useNodesT()
  const scope = useQueryScope()
  const servers = useAllServers(scope)
  const legacy = useLegacyEditors()
  const opened = useRef(false)
  const { canCreate, openCreate } = legacy
  useEffect(() => {
    if (canCreate && !opened.current) { opened.current = true; openCreate() }
  }, [canCreate, openCreate])
  if (servers.isPending || canCreate) return null
  const none = (servers.data?.length ?? 0) === 0
  return <EmptyState title={t(none ? 'nodes.create_dialog.no_servers' : 'nodes.create_dialog.no_writable_servers')}
    action={<Button variant="outline" render={<Link to={none ? '/admin/servers' : '/admin/nodes'} />}>{t(none ? 'nodes.empty.no_servers' : 'nodes.detail.back')}</Button>} />
}

export default function NodeCreateRoute() {
  const t = useNodesT()
  const admin = useCan('config.write')
  const navigate = useNavigate()
  if (!admin) return <EmptyState title={t('nodes.detail.admin_only')} action={<Button variant="outline" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>} />
  return <LegacyEditorsProvider onClosed={() => navigate('/admin/nodes')}><Wizard /></LegacyEditorsProvider>
}
