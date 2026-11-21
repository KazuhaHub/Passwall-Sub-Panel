import { useEffect, useState, type FormEvent } from 'react'
import { changeMyPassword } from '@/api/me'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useMeT } from './useMeT'

/**
 * Change the password: the old one, the new one twice. The autocomplete attributes tell a password manager which field is
 * which (`current-password` for the old, `new-password` for the other two), so it offers to fill the first and to save the
 * second rather than the reverse. A mismatch is said next to the confirm field once the person has left it, and the form
 * does not submit while it stands.
 */
export function PasswordDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useMeT()
  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) { setOldPwd(''); setNewPwd(''); setConfirmPwd(''); setTouched(false) }
  }, [open])

  const mismatch = confirmPwd.length > 0 && confirmPwd !== newPwd

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (newPwd !== confirmPwd) { setTouched(true); return }
    setBusy(true)
    try {
      await changeMyPassword(oldPwd, newPwd)
      pushSnack(t('password.saved'), 'success')
      onClose()
    } catch { /* the request layer already told the person why */ } finally { setBusy(false) }
  }

  return (
    <Dialog open={open} onOpenChange={o => { if (!o && !busy) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('password.title')}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-4">
          <Field label={t('password.old')} required>
            <Input type="password" required autoComplete="current-password" value={oldPwd} onChange={e => setOldPwd(e.target.value)} />
          </Field>
          <Field label={t('password.new')} required>
            <Input type="password" required autoComplete="new-password" value={newPwd} onChange={e => setNewPwd(e.target.value)} />
          </Field>
          <Field label={t('password.confirm')} required error={mismatch && touched ? t('password.mismatch') : undefined}>
            <Input type="password" required autoComplete="new-password" value={confirmPwd}
              onChange={e => setConfirmPwd(e.target.value)} onBlur={() => setTouched(true)} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>{t('common.cancel')}</Button>
            <AsyncButton type="submit" pending={busy}>{t('common.ok')}</AsyncButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
