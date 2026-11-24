import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { NativeServerProvisioning, Server } from '@/api/servers'
import { ObjectPage, objectErrorKind, type ObjectPageState } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useCan } from '@/utils/permissions'
import { dropInstallation, peekInstallation } from './install/installHandoff'
import { DEFAULT_INSTALLATION } from './install/installModel'
import { NativeInstallationPage } from './install/NativeInstallationPage'
import { rotateWithConfirm } from './install/rotateCredential'
import { SERVERS_PATH, parseServerId } from './serverRoutes'
import { useServerRecord } from './useServerRecord'

function InstallRecordPage({ id }: { id: number }) {
  const { t } = useTranslation(['admin', 'common'])
  const back = useBackTarget(SERVERS_PATH)
  const { server: listed, query, missing } = useServerRecord(id)
  // What the page that created or rotated the credential left, read once: the page opens on it without waiting for the list.
  const [start] = useState(() => peekInstallation(id))
  useEffect(() => { dropInstallation(id) }, [id])
  const [rotated, setRotated] = useState<{ server: Server; provisioning: NativeServerProvisioning } | null>(null)
  const [rotating, setRotating] = useState(false)
  // A rotation that finishes after the administrator left must not touch a page that is gone.
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])

  const server = rotated?.server ?? listed ?? start?.server
  const kind = !server && query.isError && !query.data ? objectErrorKind(query.error) : null
  const notFound = (!server && missing) || (!!server && server.panel_type !== 'psp')
  const state: ObjectPageState | undefined = notFound ? { kind: 'not_found', title: t('admin:servers.detail.install_not_found') }
    : kind === 'forbidden' ? { kind, title: t('admin:servers.detail.forbidden') }
    : kind ? { kind: 'error', reason: query.error, retry: () => query.refetch() }
    : !server ? { kind: 'loading' } : undefined
  if (state || !server) return <ObjectPage back={{ to: back, label: t('admin:servers.title') }} state={state} />

  async function rotate(s: Server) {
    setRotating(true)
    try {
      const provisioned = await rotateWithConfirm(t, s)
      if (provisioned && mounted.current) setRotated({ server: provisioned.server, provisioning: provisioned })
    } finally { if (mounted.current) setRotating(false) }
  }
  // A rotation keeps what the page was: still a new server, still on the method chosen for it.
  const creating = !!start?.creating
  return (
    <NativeInstallationPage server={server} initialProvisioning={rotated?.provisioning ?? start?.provisioning ?? null}
      newServer={creating} initialSelection={creating && start?.selection ? start.selection : DEFAULT_INSTALLATION}
      onRotate={s => void rotate(s)} rotating={rotating} />
  )
}

/**
 * `/admin/servers/:id/install`: the installation of one Passwall Node, for an administrator and for a real id of a
 * Passwall Node server. It reads the server from the list, but a server just created (or whose credential was just
 * rotated) opens on what the page before it handed over (see installHandoff).
 */
export default function ServerInstallRoute() {
  const { t } = useTranslation('admin')
  const back = useBackTarget(SERVERS_PATH)
  const admin = useCan('config.write')
  const id = parseServerId(useParams().id)
  const to = { to: back, label: t('admin:servers.title') }
  if (!admin) return <ObjectPage back={to} state={{ kind: 'forbidden', title: t('admin:servers.install_reinstall.forbidden') }} />
  if (!id) return <ObjectPage back={to} state={{ kind: 'not_found', title: t('admin:servers.detail.install_not_found') }} />
  return <InstallRecordPage key={id} id={id} />
}
