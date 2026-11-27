import { useEffect, useState, type FormEvent } from 'react'
import type { AxiosError } from 'axios'

import { regenerate2FARecovery, regenerateRecoveryWithPasskey } from '@/api/me'
import type { TwoFAMethod } from '@/api/types'
import { IconFingerprint } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { defaultTwoFAMethod, writeLast2FAMethod } from '@/utils/twofa'
import { RecoveryCodeList } from './RecoveryCodeList'
import { useMeT } from './me/useMeT'
import { ceremonyCancelled, webAuthnSupported } from './webauthn'

interface Props {
  open: boolean
  /** Unused recovery codes left on the account (drives the count and the "low" warning). */
  remaining: number
  /** Whether the account has a passkey: enables regenerating with a passkey assertion (no authenticator or recovery code needed). */
  hasPasskey: boolean
  /** Whether the account has TOTP on. Together with hasPasskey and remaining it decides which proof is offered first, as login does. */
  totpEnabled: boolean
  onClose: () => void
  /** Called after a successful regenerate so the parent can refresh the count. */
  onChanged: () => void
}

type Step = 'view' | 'regenerate' | 'codes'

/**
 * Recovery codes, independent of TOTP, so a passkey-only account (whose codes were issued when it added its first passkey)
 * can see how many remain and regenerate them. Regenerating is a step-up: it needs proof, an authenticator code or an
 * existing recovery code (typed) or a passkey assertion, and then shows the fresh set ONCE. The old set stops working at
 * once, which the regenerate step says before the person commits.
 */
export default function RecoveryCodesDialog({ open, remaining, hasPasskey, totpEnabled, onClose, onChanged }: Props) {
  const { t } = useMeT()
  const [step, setStep] = useState<Step>('view')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[]>([])

  // The typed-code path proves identity with a current TOTP code OR an existing recovery code, so it only helps an
  // account that has one of those. The passkey is the other proof. The default is picked the way login picks it
  // (last used in this browser, else the strongest available), instead of hard-defaulting to a code a passkey-only user does not have.
  const codePathAvailable = totpEnabled || remaining > 0
  const available: TwoFAMethod[] = []
  if (hasPasskey) available.push('passkey')
  if (totpEnabled) available.push('totp')
  if (remaining > 0) available.push('recovery')
  const passkeyPrimary = hasPasskey && defaultTwoFAMethod(available) === 'passkey'

  useEffect(() => {
    if (open) { setStep('view'); setError(''); setNote(''); setCode(''); setCodes([]) }
  }, [open])

  const toRegenerate = () => { setStep('regenerate'); setCode(''); setError(''); setNote('') }

  async function submitRegenerate(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) { setError(t('twofa.code_required')); return }
    setBusy(true); setError('')
    try {
      setCodes(await regenerate2FARecovery(c))
      // Remember the code path as the sticky default only when TOTP is the real factor; a one-time recovery code is not worth landing on next time.
      if (totpEnabled) writeLast2FAMethod('totp')
      setStep('codes')
    } catch (err) {
      setError((err as AxiosError<{ error?: string }>).response?.data?.error || t('twofa.code_invalid'))
      setCode('')
    } finally { setBusy(false) }
  }

  // Regenerate proven by a passkey assertion: no code needed, so it works for a passkey holder who lost the authenticator or has no codes left.
  async function regenViaPasskey() {
    setError(''); setNote('')
    if (!webAuthnSupported()) { setNote(t('passkey.unsupported')); return }
    setBusy(true)
    try {
      setCodes(await regenerateRecoveryWithPasskey())
      writeLast2FAMethod('passkey')
      setStep('codes')
    } catch (err) {
      if (ceremonyCancelled(err)) setNote(t('passkey.cancelled'))
      else setError((err as AxiosError<{ error?: string }>).response?.data?.error || t('twofa.generic_error'))
    } finally { setBusy(false) }
  }

  function finishCodes() {
    pushSnack(t('twofa.regen_done_toast'), 'success')
    onChanged()
    onClose()
  }

  // Freshly shown codes are not recoverable, so only the Done button closes the dialog in that step.
  const locked = step === 'codes'
  const low = remaining > 0 && remaining <= 3
  const passkeyButton = (primary: boolean) => (
    <AsyncButton variant={primary ? 'default' : 'outline'} pending={busy} icon={<IconFingerprint />} onClick={regenViaPasskey}>{t('recovery.regen_passkey')}</AsyncButton>
  )

  return (
    <Dialog open={open} onOpenChange={o => { if (!o && !busy && !locked) onClose() }}>
      <DialogContent showCloseButton={!locked}>
        <DialogHeader>
          <DialogTitle>{t('recovery.title')}</DialogTitle>
          {step === 'view' && <DialogDescription>{t('recovery.intro')}</DialogDescription>}
          {step === 'regenerate' && <DialogDescription>{t('twofa.regenerate_hint')}</DialogDescription>}
        </DialogHeader>

        {step === 'view' && (
          <>
            <Alert variant={remaining === 0 || low ? 'destructive' : 'default'} data-testid="recovery-remaining">
              <AlertDescription>
                {remaining === 0 ? t('recovery.none_left') : t('recovery.remaining', { count: remaining })}
              </AlertDescription>
            </Alert>
            {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
            {note && <Alert><AlertDescription>{note}</AlertDescription></Alert>}
            {/* The main action follows the available and last-used proof, as login does: the passkey for a holder whose
                last or only factor is a passkey, the code otherwise. */}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={onClose}>{t('twofa.cancel')}</Button>
              {passkeyPrimary ? (
                <>
                  {codePathAvailable && <Button variant="outline" disabled={busy} onClick={toRegenerate}>{t('recovery.regen_code')}</Button>}
                  {passkeyButton(true)}
                </>
              ) : (
                <>
                  {hasPasskey && passkeyButton(false)}
                  <Button disabled={busy || !codePathAvailable} onClick={toRegenerate}>{t('recovery.regen_code')}</Button>
                </>
              )}
            </DialogFooter>
          </>
        )}

        {step === 'regenerate' && (
          <form onSubmit={submitRegenerate} className="grid gap-4">
            <Field label={t('twofa.code')} required error={error || undefined}>
              <Input autoFocus autoComplete="one-time-code" placeholder="123456" value={code} onChange={e => setCode(e.target.value)} />
            </Field>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={busy} onClick={() => { setStep('view'); setCode(''); setError('') }}>{t('twofa.cancel')}</Button>
              <AsyncButton type="submit" pending={busy}>{t('twofa.regenerate')}</AsyncButton>
            </DialogFooter>
          </form>
        )}

        {step === 'codes' && (
          <>
            <RecoveryCodeList codes={codes} warning={t('twofa.recovery_warning')} />
            <DialogFooter><Button onClick={finishCodes}>{t('twofa.done')}</Button></DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
