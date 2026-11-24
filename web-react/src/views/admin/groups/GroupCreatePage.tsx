import { useRef } from 'react'
import { useLocation, useNavigate } from 'react-router'
import type { Group } from '@/api/types'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useCan } from '@/utils/permissions'
import { GroupEditor } from './detail/GroupEditor'
import { useGroupCache } from './groupCache'
import { GROUPS_PATH, groupPath } from './groupRoutes'
import { useGroupsT } from './useGroupsT'

/**
 * `/admin/groups/new`: the same six tabs as a group's page, on a group that does not exist yet (the overrides and the
 * destinations say to save first). Saving goes to the NEW group's page. Leaving with an edited form asks; finishing
 * the create does not.
 */
export default function GroupCreatePage() {
  const t = useGroupsT()
  const navigate = useNavigate()
  const location = useLocation()
  const canConfig = useCan('config.write')
  const back = { to: useBackTarget(GROUPS_PATH), label: t('admin:groups.title') }
  const cache = useGroupCache()
  // The create just finished: the draft is moot. A ref set before navigating, because the dirty state clears a render later.
  const finished = useRef(false)

  if (!canConfig) return <ObjectPage back={back} state={{ kind: 'forbidden', title: t('admin:groups.detail.staff_only') }} />

  function created(group: Group) {
    cache.remember(group)
    cache.refreshLists()
    // The new group's page opens on what the create returned, and its back link still goes to the list as it was.
    void navigate(groupPath(group.id), { replace: true, state: location.state })
  }

  return <GroupEditor group={null} back={back} bypass={finished} frame={{ title: t('admin:groups.create') }} onCreated={created} />
}
