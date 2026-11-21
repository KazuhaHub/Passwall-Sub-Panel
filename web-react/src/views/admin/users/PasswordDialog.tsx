import { useState } from 'react'
import type { User } from '@/api/types'
import { resetPassword } from '@/api/users'
import { AsyncButton } from '@/components/common/AsyncButton'
import { IconButton } from '@/components/common/IconButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { IconHide, IconRandom, IconShow } from '@/lib/icons'
import { useAdminT } from './useAdminT'

/**
 * A random one in the server's own shape (alphanumeric, mixed case, 12 characters, no look-alikes), made here so
 * the admin sees it at once and can edit it before submitting.
 */
export function randomPassword(): string {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const arr = new Uint32Array(12)
  crypto.getRandomValues(arr)
  let out = ''
  for (let i = 0; i < arr.length; i++) out += alphabet[arr[i] % alphabet.length]
  return out
}

/** The server's floor, mirrored here for instant feedback: 8 or more characters with a letter and a digit. Blank is fine (the server generates one). */
export const passwordOk = (v: string) => v === '' || /^(?=.{8,})(?=.*[A-Za-z])(?=.*\d)/.test(v)

/**
 * Set a new login password for one account, or leave it blank for a generated one. It names the account; what
 * comes back (the password the server actually set) is handed to `onDone` to be shown once.
 */
export function PasswordDialog({ user, onClose, onDone }: { user: User; onClose: () => void; onDone: (password: string) => void }) {
  const t = useAdminT()
  const [value, setValue] = useState('')
  const [shown, setShown] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    const v = value.trim()
    if (!passwordOk(v)) { setError(t('validation.password_strength')); return }
    setBusy(true)
    try {
      const res = await resetPassword(user.id, v || undefined)
      onDone(res.password)
    } catch { /* reported by the request layer; the dialog stays for a retry */ } finally { setBusy(false) }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('users.reset_password.title')} — {user.upn}</DialogTitle>
          <DialogDescription>{t('users.reset_password.hint')}</DialogDescription>
        </DialogHeader>
        <form id="reset-password" onSubmit={e => { e.preventDefault(); void submit() }}>
          <Field label={t('users.reset_password.field')} error={error || undefined}>
            {p => (
              <InputGroup>
                <InputGroupInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} autoFocus
                  type={shown ? 'text' : 'password'} autoComplete="new-password" spellCheck={false} className="font-mono"
                  value={value} onChange={e => { setValue(e.target.value); if (error) setError('') }} />
                <InputGroupAddon align="inline-end">
                  <IconButton label={t('users.reset_password.gen')} size="icon-xs" side="top" onClick={() => { setValue(randomPassword()); setShown(true); setError('') }}><IconRandom /></IconButton>
                  <IconButton label={shown ? t('users.reset_password.hide') : t('users.reset_password.show')} size="icon-xs" side="top" onClick={() => setShown(s => !s)}>{shown ? <IconHide /> : <IconShow />}</IconButton>
                </InputGroupAddon>
              </InputGroup>
            )}
          </Field>
        </form>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton type="submit" form="reset-password" pending={busy}>{t('users.reset_password.confirm')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
