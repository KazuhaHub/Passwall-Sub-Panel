import { useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import type { User } from '@/api/types'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import { useBackTarget } from '@/components/common/objectReturn'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { UsersContext } from './context'
import { CreateTab } from './CreateTab'
import { leaveSecret } from './secretHandoff'
import { USERS_PATH, userPath } from './userRoutes'
import { useAdminT } from './useAdminT'
import { useUsersWorkspace } from './useUsersWorkspace'

/**
 * `/admin/users/new`: the create form on its own page, the same fields as the Settings tab. Saving goes to the NEW
 * user's page, where the initial password and the subscription link are shown once (see `secretHandoff`). The back
 * link and the guard behave like a user's page: leaving with an edited form asks, finishing a create does not.
 */
export default function UserCreatePage() {
  const t = useAdminT()
  const navigate = useNavigate()
  const location = useLocation()
  const back = useBackTarget(USERS_PATH)
  const ws = useUsersWorkspace()
  const [dirty, setDirty] = useState(false)
  // The create just finished: the draft is moot. A ref set before navigating, because `dirty` clears a render later.
  const finished = useRef(false)
  useObjectLeaveGuard({ dirty, copy: discardChangesConfirm(t), bypass: finished })

  function created(user: User, password: string) {
    finished.current = true
    leaveSecret(user, password)
    // The new user's page opens on what the create already returned, and its back link still goes to the list as it was.
    ws.ctx.patchRow(user)
    void navigate(userPath(user.id), { replace: true, state: location.state })
  }

  return (
    <UsersContext.Provider value={ws.ctx}>
      <ObjectPage back={{ to: back, label: t('users.title') }} title={t('users.create')}>
        <div className="max-w-3xl"><CreateTab onDirtyChange={setDirty} onCreated={created} /></div>
      </ObjectPage>
    </UsersContext.Provider>
  )
}
