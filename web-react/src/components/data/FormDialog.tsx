import { useCallback, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useSpacedT } from '@/i18n/spacing'
import { DetailDialog, type DetailDialogSize } from './DetailDialog'

/**
 * A form in a dialog that a person fills in and saves or cancels: Enter submits, Esc, the close button and a
 * click outside ask first when there is an unsaved draft, and while a save is in flight nothing closes it. A
 * refused submit moves focus to the first field that names a problem. Cancel and the submit button are the
 * footer, so they stay in view while a long body scrolls.
 */
export function FormDialog({ title, description, dirty, pending, submitLabel, onSubmit, onClose, size = 'md', children }: {
  title: ReactNode
  description?: ReactNode
  dirty: boolean
  pending: boolean
  submitLabel: string
  onSubmit: () => void
  onClose: () => void
  size?: DetailDialogSize
  children: ReactNode
}) {
  const { t: c } = useSpacedT()
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
    <DetailDialog
      open
      size={size}
      title={title}
      description={description}
      onClose={() => { if (!pending) guard(onClose) }}
      onSubmit={e => { e.preventDefault(); if (!pending) { submitRef.current(); focusFirstProblem() } }}
      footer={
        <>
          <Button type="button" variant="outline" disabled={pending} onClick={() => guard(onClose)}>{c('actions.cancel')}</Button>
          <AsyncButton type="submit" pending={pending}>{submitLabel}</AsyncButton>
        </>
      }
    >
      <div ref={body} className="flex flex-col gap-4">{children}</div>
    </DetailDialog>
  )
}
