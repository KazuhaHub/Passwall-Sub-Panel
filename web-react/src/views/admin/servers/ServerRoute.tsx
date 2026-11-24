import { useParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { ServerFormPage } from './page/ServerFormPage'
import { SERVERS_PATH, parseServerId } from './serverRoutes'
import { useServerRecord } from './useServerRecord'

function ServerRecordPage({ id }: { id: number }) {
  const { t } = useTranslation('admin')
  const back = useBackTarget(SERVERS_PATH)
  const { server, query, missing } = useServerRecord(id)
  const kind = query.isError && !query.data ? objectErrorKind(query.error) : null
  const state: ObjectPageState | undefined = !server && missing ? { kind: 'not_found', title: t('admin:servers.detail.not_found') }
    : kind === 'forbidden' ? { kind, title: t('admin:servers.detail.forbidden') }
    : kind ? { kind: 'error', reason: query.error, retry: () => query.refetch() }
    : !server ? { kind: 'loading' } : undefined
  if (state || !server) return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} state={state} />
  return <ServerFormPage editing={server} />
}

/**
 * `/admin/servers/:id`: one server, its settings on a page. Only for a real id: an address that is not a positive whole
 * number is "no such server" in the page's own frame, never a request. Keyed by the id so another server starts from a
 * clean form.
 */
export default function ServerRoute() {
  const { t } = useTranslation('admin')
  const back = useBackTarget(SERVERS_PATH)
  const id = parseServerId(useParams().id)
  if (!id) return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} state={{ kind: 'not_found', title: t('admin:servers.detail.not_found') }} />
  return <ServerRecordPage key={id} id={id} />
}
