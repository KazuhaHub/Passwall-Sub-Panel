import { useParams } from 'react-router'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useCan } from '@/utils/permissions'
import GroupDetailPage from './detail/GroupDetailPage'
import { GROUPS_PATH, parseGroupId } from './groupRoutes'
import { useGroupsT } from './useGroupsT'

/**
 * `/admin/groups/:id`: only for an administrator (config.write), as the old side sheet was: every write behind this
 * page is administrator-only on the server, and an operator reads the list. An address that is not a positive whole
 * number is "no such group" in the page's own frame, never a request for /admin/groups/0.
 */
export default function GroupDetailRoute() {
  const t = useGroupsT()
  const canConfig = useCan('config.write')
  const back = { to: useBackTarget(GROUPS_PATH), label: t('admin:groups.title') }
  const id = parseGroupId(useParams().id)
  if (!canConfig) return <ObjectPage back={back} state={{ kind: 'forbidden', title: t('admin:groups.detail.staff_only') }} />
  if (id === null) return <ObjectPage back={back} state={{ kind: 'not_found', title: t('admin:groups.detail.not_found') }} />
  return <GroupDetailPage key={id} id={id} />
}
