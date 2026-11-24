import { useCallback, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { Node } from '@/api/types'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { spacedT } from '@/i18n/spacing'

/** The admin namespace's `t` with interpolation boundaries resolved (copy rule 8). Keys read `certs.…`. */
export function useCertsT(): TFunction {
  const { t } = useTranslation('admin')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(spacedT(t), [t])
}

/** The common namespace's `t`, for the words every page shares (Cancel, Save, Retry). */
export function useCommonT(): TFunction {
  const { t } = useTranslation('common')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(spacedT(t), [t])
}

/**
 * A form in a wide sheet: the three things edited here (a certificate, an ACME account, a DNS credential)
 * have credentials and long hints, which a dialog squeezed. Enter submits, Esc and the close button ask
 * first when there is an unsaved draft, and while a save is in flight nothing closes it.
 */
export function FormSheet({ title, description, dirty, pending, submitLabel, onSubmit, onClose, children }: {
  title: ReactNode
  description?: ReactNode
  dirty: boolean
  pending: boolean
  submitLabel: string
  onSubmit: () => void
  onClose: () => void
  children: ReactNode
}) {
  const c = useCommonT()
  const copy = useMemo(() => ({ title: c('ui.form.unsaved'), message: c('ui.form.unsaved_leave'), confirmText: c('ui.form.discard'), destructive: true, initialFocus: 'cancel' as const }), [c])
  const guard = useDirtyClose(dirty, copy)
  const body = useRef<HTMLDivElement>(null)

  // A refused submit leaves the first problem named under its field; take focus there instead of leaving
  // the person to find it.
  const focusFirstProblem = useCallback(() => {
    requestAnimationFrame(() => body.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
  }, [])
  const submitRef = useRef(onSubmit)
  useEffect(() => { submitRef.current = onSubmit })

  return (
    <Sheet open onOpenChange={open => { if (!open && !pending) guard(onClose) }}>
      <SheetContent side="right" className="gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:w-[36rem] data-[side=right]:sm:max-w-[36rem]">
        <form
          noValidate
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={e => { e.preventDefault(); if (!pending) { submitRef.current(); focusFirstProblem() } }}
        >
          <SheetHeader className="gap-1 border-b p-4 pr-14">
            <SheetTitle className="text-base">{title}</SheetTitle>
            {description && <SheetDescription>{description}</SheetDescription>}
          </SheetHeader>
          <div ref={body} className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">{children}</div>
          <SheetFooter className="mt-0 flex-row justify-end border-t">
            <Button type="button" variant="outline" disabled={pending} onClick={() => guard(onClose)}>{c('actions.cancel')}</Button>
            <AsyncButton type="submit" pending={pending}>{submitLabel}</AsyncButton>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}

/** The name an admin gave a node, with the server it sits on when more than one node might share it. */
export function nodeName(n: Node): string {
  return n.display_name || `#${n.id}`
}

/** Links to the nodes that deploy a certificate; the node page is where its certificate source is changed. */
export function NodeLinks({ nodes, max, className }: { nodes: readonly Node[]; max?: number; className?: string }) {
  const t = useCertsT()
  const shown = max === undefined ? nodes : nodes.slice(0, max)
  const rest = nodes.length - shown.length
  return (
    <span className={className}>
      {shown.map((n, i) => (
        <span key={n.id}>
          {i > 0 && ', '}
          <Link to={`/admin/nodes/${n.id}`} className="underline decoration-dotted underline-offset-4 hover:decoration-solid" title={`${nodeName(n)} · ${n.panel_name}`}>{nodeName(n)}</Link>
        </span>
      ))}
      {rest > 0 && <span className="text-muted-foreground">{t('certs.used_more', { count: rest })}</span>}
    </span>
  )
}
