import { useRef, useState, type ReactNode } from 'react'
import { isAxiosError } from 'axios'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { frontErrorOf } from '@/api/fronts'
import type { DeleteImpactLine } from '@/api/nodes'
import { deleteServer, getServerDeleteImpact } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { ErrorState, describeError } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { serverKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { ImpactDialogFrame } from './ImpactDialogFrame'
import { serverImpactModel } from './impactModel'
import { ImpactPathList } from '../shared/ImpactPathList'
import { useNodesT } from '../shared/useNodesT'

// The refusals the server can answer a delete with when something is still attached to the server.
const DEPENDENCY_REFUSALS = ['server_has_nodes', 'server_has_lines', 'server_used_by_routes']

const linkClass = 'text-xs underline underline-offset-2 hover:text-foreground'

function Section({ title, children, testId }: { title: string; children: ReactNode; testId: string }) {
  return (
    <div data-testid={testId}>
      <p className="mb-1.5 font-medium">{title}</p>
      {children}
    </div>
  )
}

/**
 * Deleting a server (design 5.4). It reads what is still attached before offering anything: the nodes on the
 * server, the lines that enter through it, the routes that leave through it (each of these blocks the delete, with
 * a link to where it is fixed) and the nodes that use it as their exit (a warning only: the server does not refuse
 * that). When something blocks there is no delete button. A refusal at delete time means the state changed after
 * the read, so the dialog reads again and shows the new picture instead of a toast.
 */
export function ServerDeleteDialog({ server, onClose, onDeleted }: {
  server: { id: number; name: string }
  onClose: () => void
  onDeleted: () => void
}) {
  const t = useNodesT()
  const { t: tc } = useTranslation('common')
  const scope = useQueryScope()
  const q = useQuery({
    queryKey: [...serverKeys.all(scope), 'delete-impact', server.id],
    queryFn: ({ signal }) => getServerDeleteImpact(server.id, signal),
    staleTime: 0, gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  })
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [busy, setBusy] = useState(false)
  const [changed, setChanged] = useState(false)
  // The refusal a delete answered with, kept for when the re-read that should replace it fails too.
  const [refusal, setRefusal] = useState<{ message: string; lines: DeleteImpactLine[] } | null>(null)
  const close = () => { if (!busy) onClose() }

  const send = async () => {
    setBusy(true)
    try {
      await deleteServer(server.id, { quiet: true })
      onDeleted()
    } catch (err) {
      setBusy(false)
      const e = frontErrorOf(err)
      if (e && e.status === 409 && DEPENDENCY_REFUSALS.includes(e.code)) {
        // Something attached since the read: show it, with its links, rather than a one-line toast.
        setChanged(true)
        const body = (isAxiosError(err) ? err.response?.data : undefined) as { lines?: DeleteImpactLine[] } | undefined
        setRefusal({ message: e.message, lines: body?.lines ?? [] })
        void q.refetch()
      } else {
        pushToast({ message: e?.message || t('servers.toast.delete_failed'), severity: 'error' })
      }
    }
  }

  const title = t('servers.delete.title', { name: server.name })
  if (q.isPending) {
    return <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={onClose}><p role="status" className="text-muted-foreground">{t('servers.delete.loading')}</p></ImpactDialogFrame>
  }
  if (q.isError && !q.data) {
    return (
      <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={close} cancelDisabled={busy}
        actions={(
          <>
            <Button variant="outline" disabled={busy} onClick={() => void q.refetch()}>{tc('actions.retry')}</Button>
            <AsyncButton variant="destructive" onClick={send}>{t('servers.delete.blind_delete')}</AsyncButton>
          </>
        )}>
        <ErrorState compact title={t('servers.delete.failed_title')} reason={describeError(q.error)} hint={t('servers.delete.failed_hint')} />
        {refusal && (
          <div data-testid="server-refusal" className="flex flex-col gap-2">
            <p className="font-medium">{refusal.message}</p>
            {refusal.lines.length > 0 && <ImpactPathList lines={refusal.lines} entryOf={() => server.name} />}
          </div>
        )}
      </ImpactDialogFrame>
    )
  }

  const m = serverImpactModel(q.data!)
  const blocked = m.blocking > 0
  const lineKey = (l: { node_id: number; line_id: string }) => `${l.node_id}.${l.line_id}`
  const pending = new Set(m.lines.filter(l => l.landing_pending_delete).map(lineKey))

  return (
    <ImpactDialogFrame
      title={blocked ? t('servers.delete.blocked_title', { name: server.name, count: m.blocking }) : title}
      description={blocked ? t('servers.delete.blocked_hint') : undefined}
      cancelRef={cancelRef} cancelLabel={blocked ? tc('actions.close') : undefined} onClose={close} cancelDisabled={busy} wide
      actions={blocked ? undefined : <AsyncButton variant="destructive" onClick={send}>{t('servers.delete.confirm')}</AsyncButton>}
    >
      {changed && <p role="status" className="text-muted-foreground">{t('servers.delete.changed')}</p>}

      {m.nodes.length > 0 && (
        <Section title={t('servers.delete.nodes')} testId="server-nodes">
          <ul className="flex flex-col gap-1.5">
            {m.nodes.map(n => (
              <li key={n.node_id} className="flex flex-wrap items-baseline gap-x-3">
                {n.tunnel_line_of_node_id ? (
                  <>
                    <span>{t('servers.delete.listener_of', { landing: n.tunnel_line_of ?? '', line: n.tunnel_line ?? '' })}</span>
                    <span className="text-xs text-muted-foreground">{t('servers.delete.listener_fix')}</span>
                    <Link className={linkClass} to={`/admin/nodes/${n.tunnel_line_of_node_id}`}>{t('servers.delete.open_node')}</Link>
                  </>
                ) : (
                  <>
                    <span>{n.node}</span>
                    <Link className={linkClass} to={`/admin/nodes/${n.node_id}`}>{t('servers.delete.open_node')}</Link>
                  </>
                )}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {m.lines.length > 0 && (
        <Section title={t('servers.delete.lines')} testId="server-lines">
          <ImpactPathList
            lines={m.lines as unknown as DeleteImpactLine[]}
            entryOf={() => server.name}
            extra={l => (
              <span className="flex flex-wrap items-baseline gap-x-3 text-xs text-muted-foreground">
                {pending.has(lineKey(l)) ? <span>{t('servers.delete.pending_landing')}</span> : <span>{t('servers.delete.line_fix')}</span>}
                <Link className={linkClass} to={`/admin/nodes?line=${l.node_id}.${l.line_id}`}>{t('servers.delete.open_line')}</Link>
                {pending.has(lineKey(l)) && <Link className={linkClass} to="/admin/sync-tasks">{t('servers.delete.sync_tasks')}</Link>}
              </span>
            )}
          />
        </Section>
      )}

      {m.routes.length > 0 && (
        <Section title={t('servers.delete.routes')} testId="server-routes">
          <ul className="flex flex-col gap-1.5">
            {m.routes.map(r => (
              <li key={r.node_id} className="flex flex-wrap items-baseline gap-x-3">
                <span>{r.node}</span>
                <span className="font-mono text-xs text-muted-foreground">{t('servers.delete.routes_of', { routes: r.routes.join(', ') })}</span>
                <Link className={linkClass} to={`/admin/nodes/${r.node_id}?tab=exit`}>{t('servers.delete.open_exit')}</Link>
              </li>
            ))}
          </ul>
        </Section>
      )}

      {m.egress.length > 0 && (
        <Section title={t('servers.delete.egress')} testId="server-egress">
          <p className="mb-1.5 text-muted-foreground">{t('servers.delete.egress_warning')}</p>
          <ul className="flex flex-col gap-1.5">
            {m.egress.map(n => (
              <li key={n.node_id} className="flex flex-wrap items-baseline gap-x-3">
                <span>{n.node}</span>
                <Link className={linkClass} to={`/admin/nodes/${n.node_id}?tab=exit`}>{t('servers.delete.open_exit')}</Link>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </ImpactDialogFrame>
  )
}
