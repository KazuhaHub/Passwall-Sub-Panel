import { useRef, useState, type ReactNode } from 'react'
import { AsyncButton } from '@/components/common/AsyncButton'
import { describeError } from '@/components/feedback/ErrorState'
import { ImpactDialogFrame } from '../nodes/delete/ImpactDialogFrame'
import { useCertsT, useCommonT } from './shared'

/**
 * Deleting a certificate, an ACME account or a DNS credential. It says what the thing is used by and what
 * happens to those, BEFORE the button: when something still uses it and the server would refuse, the dialog
 * offers no delete at all, only the way to resolve it. When the server refuses anyway (what it knows is
 * fresher than this list), its own reason is shown here with the same way forward, not as a bare toast.
 * Focus opens on Cancel.
 */
export function DeleteDialog({ title, name, uses, blocked, consequence, resolve, onDelete, onClose }: {
  title: string
  /** The thing being deleted, named in the question. */
  name: string
  /** What uses it (links or names); null when nothing is known to. */
  uses: ReactNode | null
  /** Something uses it and the server refuses: no delete button. */
  blocked: boolean
  /** What happens to the users when it goes anyway (a credential has no server-side guard). */
  consequence?: string
  /** How to resolve a refusal. */
  resolve: string
  onDelete: () => Promise<void>
  onClose: () => void
}) {
  const t = useCertsT()
  const c = useCommonT()
  const cancel = useRef<HTMLButtonElement>(null)
  const [refusal, setRefusal] = useState<string | null>(null)
  return (
    <ImpactDialogFrame
      title={title}
      cancelRef={cancel}
      cancelLabel={blocked ? c('actions.close') : c('actions.cancel')}
      onClose={onClose}
      description={blocked ? t('certs.delete.blocked', { name }) : t('certs.delete.question', { name })}
      actions={blocked ? undefined : (
        <AsyncButton
          variant="destructive"
          onClick={async () => {
            setRefusal(null)
            try { await onDelete() } catch (e) { setRefusal(describeError(e) ?? t('certs.delete.refused')) }
          }}
        >
          {c('actions.delete')}
        </AsyncButton>
      )}
    >
      {uses && <div><div className="mb-1 text-xs font-medium text-muted-foreground">{t('certs.delete.used_by')}</div><div className="break-words">{uses}</div></div>}
      {!blocked && uses && consequence && <p className="text-muted-foreground">{consequence}</p>}
      {blocked && <p className="text-muted-foreground">{resolve}</p>}
      {refusal && (
        <div role="alert" className="rounded-md border border-destructive/40 p-3">
          <p className="font-mono text-xs break-words text-destructive">{refusal}</p>
          <p className="mt-1 text-muted-foreground">{resolve}</p>
        </div>
      )}
    </ImpactDialogFrame>
  )
}
