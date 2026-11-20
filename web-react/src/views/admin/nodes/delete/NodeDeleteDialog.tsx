import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { DeleteImpactLine, NodeDeleteImpact } from '@/api/nodes'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { ErrorState, describeError } from '@/components/feedback/ErrorState'
import type { TunnelPlanPresenter, TunnelPlanView } from '@/utils/tunnelConfirm'
import { useNodesT } from '../shared/useNodesT'
import { ImpactBody } from './ImpactBody'
import { ImpactDialogFrame } from './ImpactDialogFrame'
import { impactViewOf, planCovered, type DeleteMode } from './impactModel'
import { runRemoval } from './runRemoval'
import { useNodeDeleteImpact } from './useDeleteImpact'

export interface NodeDeleteTarget { nodeId: number; name: string; mode: DeleteMode }

/**
 * Deleting a node, or removing it from the panel only (design 5.2). It reads what the deletion takes with it
 * BEFORE offering the button, draws every line that goes, and names the verb and the count on the button. The
 * focus opens on Cancel and there is no typed-name step. When the read fails it says so and offers a retry; a
 * delete without the read is possible but is a separate, plainly worded button, never the default.
 */
export function NodeDeleteDialog({ target, entryOf, serverName, onDisable, onClose, onDone }: {
  target: NodeDeleteTarget
  entryOf: (line: DeleteImpactLine) => string | undefined
  serverName: (panelId: number) => string
  /** Offered beside a delete when the node can simply be turned off instead. */
  onDisable?: () => Promise<void>
  onClose: () => void
  /** Called once the server has accepted the request; the host closes the dialog and refreshes. */
  onDone: () => void
}) {
  const t = useNodesT()
  const { t: tc } = useTranslation('common')
  const { nodeId, name, mode } = target
  const q = useNodeDeleteImpact(nodeId)
  const view = impactViewOf(q, mode)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [busy, setBusy] = useState(false)
  const [plan, setPlan] = useState<{ view: TunnelPlanView; answer: (ok: boolean) => void } | null>(null)
  const shown = useRef<NodeDeleteImpact['tunnels']>([])
  shown.current = q.data?.tunnels ?? []
  const abort = useRef<AbortController | null>(null)
  const answerRef = useRef<((ok: boolean) => void) | null>(null)

  // Closing the dialog while the server's plan waits for an answer declines it, so the request ends instead of hanging.
  // The controller is made when the dialog mounts, not once per component instance: in development React mounts,
  // unmounts and mounts again, and a controller made earlier would arrive at the second mount already aborted.
  useEffect(() => {
    const controller = new AbortController()
    abort.current = controller
    return () => { controller.abort(); answerRef.current?.(false) }
  }, [])

  const present: TunnelPlanPresenter = plan0 => {
    if (planCovered(plan0, shown.current)) return Promise.resolve(true)
    return new Promise<boolean>(resolve => {
      const answer = (ok: boolean) => { answerRef.current = null; setPlan(null); resolve(ok) }
      answerRef.current = answer
      setPlan({ view: plan0, answer })
    })
  }

  const send = async () => {
    setBusy(true)
    try {
      await runRemoval(mode, nodeId, present, abort.current?.signal)
      onDone()
    } catch {
      // The request layer has already said why. The state may have changed under the dialog (a new route, a new
      // line), so what the dialog shows is read again rather than trusted.
      if (!abort.current?.signal.aborted) { setBusy(false); void q.refetch() }
    }
  }

  const closeOrDecline = () => {
    if (plan) { plan.answer(false); return }
    if (!busy) onClose()
  }

  const title = t(mode === 'delete' ? 'nodes.impact.node_title' : 'nodes.delete.remove_title', { name })
  const verbKey = mode === 'delete' ? 'nodes.impact.confirm' : 'nodes.impact.remove_confirm'

  if (plan) {
    return (
      <ImpactDialogFrame title={t('nodes.impact.plan_title')} cancelRef={cancelRef} onClose={closeOrDecline}
        actions={<Button variant="destructive" onClick={() => plan.answer(true)}>{t('nodes.impact.plan_confirm')}</Button>}>
        <p className="text-muted-foreground">{t('nodes.impact.plan_body')}</p>
        <ul className="list-disc ps-5" data-testid="impact-plan">{plan.view.lines.map((l, i) => <li key={i}>{l}</li>)}</ul>
      </ImpactDialogFrame>
    )
  }

  if (view.kind === 'blocked') {
    return (
      <ImpactDialogFrame title={t('nodes.impact.blocked_title', { name })} cancelRef={cancelRef} cancelLabel={tc('actions.close')} onClose={onClose}
        actions={<Button render={<Link to="/admin/servers" />}>{t('nodes.impact.go_servers')}</Button>}>
        <p>{t('nodes.impact.blocked_body')}</p>
        {/* The server words these in English and names no server or port to link to (design gap G2): shown as given. */}
        <ul className="list-disc ps-5 font-mono text-xs break-words" data-testid="blocked-routes">{view.routes.map(r => <li key={r}>{r}</li>)}</ul>
        <p className="text-muted-foreground">{t('nodes.impact.blocked_fix')}</p>
      </ImpactDialogFrame>
    )
  }

  if (view.kind === 'failed') {
    return (
      <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={closeOrDecline} cancelDisabled={busy}
        actions={(
          <>
            <Button variant="outline" disabled={busy} onClick={() => void q.refetch()}>{tc('actions.retry')}</Button>
            <AsyncButton variant="destructive" onClick={send}>{t(mode === 'delete' ? 'nodes.impact.blind_delete' : 'nodes.impact.blind_remove')}</AsyncButton>
          </>
        )}>
        <ErrorState compact title={t('nodes.impact.failed_title')} reason={describeError(view.reason)} hint={t('nodes.impact.failed_hint')} />
      </ImpactDialogFrame>
    )
  }

  if (view.kind === 'loading') {
    return (
      <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={onClose}>
        <p role="status" className="text-muted-foreground">{t('nodes.impact.loading')}</p>
      </ImpactDialogFrame>
    )
  }

  const { impact } = view
  return (
    <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={closeOrDecline} cancelDisabled={busy}
      actions={<AsyncButton variant="destructive" onClick={send}>{t(verbKey, { count: impact.lines.length })}</AsyncButton>}>
      <ImpactBody impact={impact} entryOf={entryOf} serverName={serverName} mode={mode} />
      <p className="text-muted-foreground">{t(mode === 'delete' ? 'nodes.impact.body' : 'nodes.delete.remove_body')}</p>
      {mode === 'delete' && onDisable && (
        <p className="flex flex-wrap items-center gap-x-1 text-muted-foreground">
          {t('nodes.impact.prefer_disable')}
          <AsyncButton variant="link" size="sm" className="h-auto p-0" disabled={busy} onClick={onDisable}>{t('nodes.impact.disable_instead')}</AsyncButton>
        </p>
      )}
    </ImpactDialogFrame>
  )
}
