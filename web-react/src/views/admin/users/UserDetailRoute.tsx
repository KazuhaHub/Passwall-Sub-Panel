import { useParams } from 'react-router'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import UserDetailPage from './detail/UserDetailPage'
import { USERS_PATH } from './userRoutes'
import { useAdminT } from './useAdminT'

/**
 * `/admin/users/:id`: staff (administrators and operators, as the list is), and only for a real id. An address that
 * is not a positive whole number is "no such user" in the page's own frame, never a request for /admin/users/0.
 */
export default function UserDetailRoute() {
  const t = useAdminT()
  const back = useBackTarget(USERS_PATH)
  const { id } = useParams()
  const n = /^\d+$/.test(id ?? '') ? Number(id) : 0
  if (!Number.isSafeInteger(n) || n <= 0) {
    return <ObjectPage back={{ to: back, label: t('users.title') }} state={{ kind: 'not_found', title: t('users.detail.not_found') }} />
  }
  return <UserDetailPage key={n} id={n} />
}
