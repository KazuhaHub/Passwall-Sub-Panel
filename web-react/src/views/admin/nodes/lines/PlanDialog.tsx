import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { TunnelPlanPresenter, TunnelPlanView } from '@/utils/tunnelConfirm'
import { useNodesT } from '../shared/useNodesT'

/** The server's tunnel plan as a list of facts: each created or removed listener and port, then the refresh warning. */
export function PlanLines({ lines }: { lines: readonly string[] }) {
  return (
    <ul className="list-disc space-y-1 pl-5 text-sm" data-testid="tunnel-plan">
      {lines.map((l, i) => <li key={i}>{l}</li>)}
    </ul>
  )
}

/**
 * A presenter for `withTunnelConfirm` that shows the plan in a dialog of this page, in place of the shared
 * confirmation. `dialog` must be rendered by the caller; `present` is what the api functions take as
 * `{ present }`.
 *
 * It always settles: a second plan replaces the first (which is declined), and unmounting declines the one
 * being asked. A presenter that never settled would leave the save waiting for ever.
 */
export function usePlanPresenter() {
  const t = useNodesT()
  const [asked, setAsked] = useState<TunnelPlanView | null>(null)
  const resolver = useRef<((ok: boolean) => void) | null>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)

  const settle = useCallback((ok: boolean) => {
    const r = resolver.current
    resolver.current = null
    setAsked(null)
    r?.(ok)
  }, [])
  const present = useCallback<TunnelPlanPresenter>(plan => new Promise<boolean>(resolve => {
    resolver.current?.(false)
    resolver.current = resolve
    setAsked(plan)
    // The caller gave up (its own sheet or dialog is gone): the question goes with it.
    plan.signal?.addEventListener('abort', () => { if (resolver.current === resolve) settle(false) }, { once: true })
  }), [settle])
  useEffect(() => () => { resolver.current?.(false); resolver.current = null }, [])

  const dialog: ReactNode = asked && (
    <Dialog open onOpenChange={open => { if (!open) settle(false) }}>
      <DialogContent initialFocus={cancelRef}>
        <DialogHeader>
          <DialogTitle>{asked.title}</DialogTitle>
          <DialogDescription className="sr-only">{asked.title}</DialogDescription>
        </DialogHeader>
        <PlanLines lines={asked.lines} />
        <DialogFooter>
          <Button ref={cancelRef} variant="outline" onClick={() => settle(false)}>{t('common:actions.cancel')}</Button>
          <Button onClick={() => settle(true)}>{t('nodes.line.plan_confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
  return { present, dialog }
}
