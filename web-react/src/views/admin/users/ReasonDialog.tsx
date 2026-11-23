import { useState } from 'react'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { useAdminT } from './useAdminT'

/**
 * Enable or disable the account with an optional reason, for one user or for a batch. It names who it is about,
 * and says what the choice does. It stays open, its button pending and its dismissals refused, until the request
 * settles: closing it mid-request would hide the only sign the change is still under way, and a failure (already
 * toasted) must be retryable without typing the reason again.
 */
export function ReasonDialog({ subject, enable, onClose, onSubmit }: {
  /** The account's name, or the number of accounts in a batch. */
  subject: { upn: string } | { count: number }
  enable: boolean
  onClose: () => void
  /** Resolves true when it worked and the dialog may close. */
  onSubmit: (reason: string) => Promise<boolean>
}) {
  const t = useAdminT()
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const who = 'upn' in subject ? subject.upn : t('users.batch.selected', { count: subject.count })

  async function submit() {
    setBusy(true)
    try { if (await onSubmit(reason)) onClose() } finally { setBusy(false) }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(enable ? 'users.reason.enable_title' : 'users.reason.disable_title')} — {who}</DialogTitle>
          <DialogDescription>{t(enable ? 'users.reason.enable_hint' : 'users.reason.disable_hint')}</DialogDescription>
        </DialogHeader>
        <Field label={t('users.reason.label')}>
          <Textarea rows={3} autoFocus value={reason} onChange={e => setReason(e.target.value)}
            placeholder={t(enable ? 'users.reason.enable_placeholder' : 'users.reason.disable_placeholder')} />
        </Field>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton variant={enable ? 'default' : 'destructive'} pending={busy} onClick={submit}>
            {t(enable ? 'users.action.enable' : 'users.action.disable')}
          </AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
