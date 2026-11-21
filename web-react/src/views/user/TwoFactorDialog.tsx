import { useEffect, useState, type FormEvent } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import type { AxiosError } from 'axios'

import { begin2FA, disable2FA, disableTOTPWithPasskey, enable2FA } from '@/api/me'
import { IconFingerprint } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyButton } from '@/components/common/CopyButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { RecoveryCodeList } from './RecoveryCodeList'
import { useMeT } from './me/useMeT'
import { ceremonyCancelled, webAuthnSupported } from './webauthn'

interface Props {
  open: boolean
  /** Whether 2FA is currently enabled on the account (drives enroll vs disable). */
  enabled: boolean
  /** Whether the account has a passkey: enables turning TOTP off with a passkey assertion (for a user who lost the authenticator). */
  hasPasskey: boolean
  /** Accepted and ignored. The enrolment page still passes the colour tokens the old dialog needed. */
  md?: unknown
  onClose: () => void
  /** Called after a successful enable/disable so the parent can refresh the profile. */
  onChanged: () => void
}

type Step = 'intro' | 'enroll' | 'recovery' | 'disable'

/**
 * The authenticator (TOTP) flow in one dialog:
 *   off -> intro -> (begin) enroll: QR + code -> (enable) recovery codes -> done
 *   on  -> disable: a current code, or a passkey assertion
 * Recovery-code regeneration lives in RecoveryCodesDialog, so turning it off stays a single-purpose step.
 */
export default function TwoFactorDialog({ open, enabled, hasPasskey, onClose, onChanged }: Props) {
  const { t } = useMeT()
  const [step, setStep] = useState<Step>(enabled ? 'disable' : 'intro')
  const [busy, setBusy] = useState(false)
  // A wrong or missing code is said under the code field; anything else (an expired begin-session, a server fault) is not
  // about the field and is said above the buttons.
  const [codeError, setCodeError] = useState('')
  const [error, setError] = useState('')
  // A cancelled or unsupported passkey prompt: told calmly, not as an error.
  const [note, setNote] = useState('')
  const [otpauthURL, setOtpauthURL] = useState('')
  const [secret, setSecret] = useState('')
  const [code, setCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])

  // Start from the right step every time the dialog (re)opens.
  useEffect(() => {
    if (open) {
      setStep(enabled ? 'disable' : 'intro')
      setCodeError(''); setError(''); setNote(''); setCode(''); setOtpauthURL(''); setSecret(''); setRecoveryCodes([])
    }
  }, [open, enabled])

  const reason = (err: unknown) => (err as AxiosError<{ error?: string }>).response?.data?.error || t('twofa.generic_error')

  /** A 401 means the code itself was wrong (the localized message); anything else carries a real reason worth showing, not a misleading "invalid code". */
  function showCodeFailure(err: unknown) {
    if ((err as AxiosError).response?.status === 401) { setCodeError(t('twofa.code_invalid')); setCode('') }
    else setError(reason(err))
  }

  async function startEnroll() {
    setBusy(true); setError('')
    try {
      const begun = await begin2FA()
      setOtpauthURL(begun.otpauth_url)
      setSecret(begun.secret)
      setStep('enroll')
    } catch (err) {
      setError(reason(err))
    } finally { setBusy(false) }
  }

  async function confirmEnroll(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) { setCodeError(t('twofa.code_required')); return }
    setBusy(true); setError(''); setCodeError('')
    try {
      setRecoveryCodes(await enable2FA(c))
      setStep('recovery')
    } catch (err) {
      showCodeFailure(err)
    } finally { setBusy(false) }
  }

  async function submitDisable(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) { setCodeError(t('twofa.code_required')); return }
    setBusy(true); setError(''); setCodeError('')
    try {
      await disable2FA(c)
      pushSnack(t('twofa.disabled_toast'), 'success')
      onChanged()
      onClose()
    } catch (err) {
      showCodeFailure(err)
    } finally { setBusy(false) }
  }

  // Turning TOTP off proven by a passkey assertion: for someone who has a passkey but no longer has the authenticator
  // (or a recovery code) to type a code from. A cancelled browser prompt is not an error.
  async function disableViaPasskey() {
    setError(''); setCodeError(''); setNote('')
    if (!webAuthnSupported()) { setNote(t('passkey.unsupported')); return }
    setBusy(true)
    try {
      await disableTOTPWithPasskey()
      pushSnack(t('twofa.disabled_toast'), 'success')
      onChanged()
      onClose()
    } catch (err) {
      if (ceremonyCancelled(err)) setNote(t('passkey.cancelled'))
      else setError(reason(err))
    } finally { setBusy(false) }
  }

  function finishRecovery() {
    pushSnack(t('twofa.enabled_toast'), 'success')
    onChanged()
    onClose()
  }

  // In the recovery step nothing but the Done button may close the dialog. The codes are stored server-side only as hashes,
  // so a stray click outside or Escape would lose them for good, and would skip finishRecovery, leaving the profile showing
  // the authenticator "off" although it is now on.
  const locked = step === 'recovery'
  const intro = { intro: t('twofa.intro'), enroll: t('twofa.scan_hint'), disable: t('twofa.disable_hint') } as const
  const alert = (
    <>
      {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
      {note && <Alert><AlertDescription>{note}</AlertDescription></Alert>}
    </>
  )

  return (
    <Dialog open={open} onOpenChange={o => { if (!o && !busy && !locked) onClose() }}>
      <DialogContent showCloseButton={!locked}>
        <DialogHeader>
          <DialogTitle>{t('twofa.title')}</DialogTitle>
          {/* The recovery step's explanation is the warning drawn with the codes, so it is not said twice. */}
          {step !== 'recovery' && <DialogDescription>{intro[step]}</DialogDescription>}
        </DialogHeader>

        {step === 'intro' && (
          <>
            {alert}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={onClose}>{t('twofa.cancel')}</Button>
              <AsyncButton pending={busy} onClick={startEnroll}>{t('twofa.start')}</AsyncButton>
            </DialogFooter>
          </>
        )}

        {step === 'enroll' && (
          <form onSubmit={confirmEnroll} className="grid gap-4">
            {/* A QR code needs a white ground in both themes. */}
            <div className="mx-auto rounded-lg border bg-white p-3 leading-none">{otpauthURL && <QRCodeSVG value={otpauthURL} size={176} />}</div>
            <div>
              <p className="text-xs text-muted-foreground">{t('twofa.manual_hint')}</p>
              <div className="mt-1 flex items-center gap-1">
                <code data-testid="totp-secret" className="min-w-0 flex-1 font-mono text-sm break-all">{secret}</code>
                <CopyButton value={secret} name={t('twofa.title')} />
              </div>
            </div>
            <Field label={t('twofa.code')} required error={codeError || undefined}>
              <Input autoFocus inputMode="numeric" autoComplete="one-time-code" placeholder="123456" value={code} onChange={e => setCode(e.target.value)} />
            </Field>
            {alert}
            <DialogFooter>
              <Button type="button" variant="outline" disabled={busy} onClick={onClose}>{t('twofa.cancel')}</Button>
              <AsyncButton type="submit" pending={busy}>{t('twofa.confirm')}</AsyncButton>
            </DialogFooter>
          </form>
        )}

        {step === 'recovery' && (
          <>
            <RecoveryCodeList codes={recoveryCodes} warning={t('twofa.recovery_warning')} />
            <DialogFooter><Button onClick={finishRecovery}>{t('twofa.done')}</Button></DialogFooter>
          </>
        )}

        {step === 'disable' && (
          <form onSubmit={submitDisable} className="grid gap-4">
            <Field label={t('twofa.code')} required hint={t('twofa.disable_code_hint')} error={codeError || undefined}>
              <Input autoFocus autoComplete="one-time-code" placeholder="123456" value={code} onChange={e => setCode(e.target.value)} />
            </Field>
            {hasPasskey && (
              <Button type="button" variant="outline" className="self-start" disabled={busy} onClick={() => void disableViaPasskey()}>
                <IconFingerprint />{t('twofa.disable_passkey')}
              </Button>
            )}
            {alert}
            <DialogFooter>
              <Button type="button" variant="outline" disabled={busy} onClick={onClose}>{t('twofa.cancel')}</Button>
              <AsyncButton type="submit" variant="destructive" pending={busy}>{t('twofa.disable')}</AsyncButton>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
