import { Navigate, useLocation, useNavigate, useParams, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { ObjectPage, type ObjectBack } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useCan } from '@/utils/permissions'
import { ACCESS_PATH, listPath, parseNewListKind, parseObjectId } from '../accessRoutes'
import ListEditor from './ListEditor'

function useBack(): ObjectBack {
  const { t } = useTranslation(['admin'])
  return { to: useBackTarget(ACCESS_PATH), label: t('admin:access_control.title') }
}

/**
 * `/admin/access-control/lists/:id`. An id that is not a positive whole number is "no such list" in the page's own
 * frame, never a request for list #0. The editor is keyed by the id so each list starts from its own read.
 */
export function ListDetailRoute() {
  const { t } = useTranslation(['admin'])
  const back = useBack()
  const canView = useCan('access.view')
  const n = parseObjectId(useParams().id)
  if (!canView) return <Navigate to="/admin/dashboard" replace />
  if (n === null) return <ObjectPage back={back} state={{ kind: 'not_found', title: t('admin:access_control.lists.dialog.not_found') }} />
  return <ListEditor key={n} editId={n} back={back} onCreated={() => undefined} />
}

/** `/admin/access-control/lists/new`, optionally `?kind=remote|geosite` (the kind the "New list" menu chose). */
export function ListCreateRoute() {
  const back = useBack()
  const navigate = useNavigate()
  const location = useLocation()
  const canView = useCan('access.view')
  const kind = parseNewListKind(useSearchParams()[0].get('kind'))
  if (!canView) return <Navigate to="/admin/dashboard" replace />
  // The new list's page keeps this page's back link (state is carried) and takes the form's place in history.
  return <ListEditor key={kind} editId={null} initialKind={kind} back={back} onCreated={id => void navigate(listPath(id), { replace: true, state: location.state })} />
}
