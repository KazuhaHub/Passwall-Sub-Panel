import { useRef, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { resetPassword } from '@/api/auth'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { PasswordField } from '@/views/auth/PasswordField'
import { BackToLogin, FormAlert, focusFirstInvalid } from '@/views/auth/parts'

// A password meets the panel floor: >=8 chars with at least one letter and one
// digit (mirrors the backend's IsMinimallyStrongPassword).
function strongEnough(pw: string): boolean {
  return pw.length >= 8 && /[a-zA-Z]/.test(pw) && /[0-9]/.test(pw)
}

export default function ResetPasswordView() {
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const form = useRef<HTMLFormElement>(null)

  // A token in the URL → link delivery. Otherwise the user types their
  // username + the OTP code they received.
  const token = params.get('token') ?? ''
  const otpMode = token === ''

  const [ident, setIdent] = useState('')
  const [code, setCode] = useState('')
  const [pw, setPw] = useState('')
  const [confirm, setConfirm] = useState('')
  const [pwSeen, setPwSeen] = useState(false)
  const [confirmSeen, setConfirmSeen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')

  const pwError = pwSeen && pw && !strongEnough(pw)
    ? t('auth:password_too_weak', { defaultValue: '密码至少 8 位，且含字母和数字' }) : undefined
  const confirmError = confirmSeen && confirm && pw !== confirm ? t('auth:reset_password_mismatch') : undefined

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setPwSeen(true)
    setConfirmSeen(true)
    if (pw !== confirm || !strongEnough(pw)) { focusFirstInvalid(form.current); return }
    if (otpMode && (!ident.trim() || !code.trim())) return
    setBusy(true)
    try {
      await resetPassword(otpMode
        ? { ident: ident.trim(), code: code.trim(), new_password: pw }
        : { token, new_password: pw })
      setDone(true)
    } catch (err) {
      const status = (err as AxiosError).response?.status
      setError(status === 401 ? t('auth:reset_password_invalid_token') : t('auth:reset_password_error'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthLayout title={t('auth:reset_password_title')} subtitle={t('auth:reset_password_subtitle')}>
      {done ? (
        <>
          <FormAlert tone="success">{t('auth:reset_password_success')}</FormAlert>
          <Button size="lg" className="w-full" onClick={() => navigate('/login')}>
            {t('auth:logged_out_sign_in_again')}
          </Button>
        </>
      ) : (
        <>
          <form ref={form} onSubmit={submit} noValidate className="flex flex-col gap-4">
            {otpMode && (
              <>
                <Field label={t('auth:forgot_password_upn_label')}>
                  <Input name="username" value={ident} onChange={e => setIdent(e.target.value)} autoComplete="username" autoFocus />
                </Field>
                <Field label={t('auth:reset_password_code_label', { defaultValue: '验证码' })}>
                  <Input name="code" value={code} onChange={e => setCode(e.target.value)}
                    autoComplete="one-time-code" inputMode="numeric" maxLength={8} />
                </Field>
              </>
            )}
            <Field label={t('auth:reset_password_new_label')} error={pwError}>
              <PasswordField name="password" value={pw} onChange={e => setPw(e.target.value)}
                onBlur={() => setPwSeen(true)} autoComplete="new-password" autoFocus={!otpMode} />
            </Field>
            <Field label={t('auth:reset_password_confirm_label')} error={confirmError}>
              <PasswordField name="password_confirm" value={confirm} onChange={e => setConfirm(e.target.value)}
                onBlur={() => setConfirmSeen(true)} autoComplete="new-password" />
            </Field>
            {error && <FormAlert tone="error">{error}</FormAlert>}
            <AsyncButton type="submit" size="lg" className="w-full" pending={busy}>
              {t('auth:reset_password_submit')}
            </AsyncButton>
          </form>
          <BackToLogin />
        </>
      )}
    </AuthLayout>
  )
}
