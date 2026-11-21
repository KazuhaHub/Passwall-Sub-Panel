import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { getAuthMethods, requestPasswordReset } from '@/api/auth'
import type { AuthMethods, LoginCaptcha } from '@/api/types'
import CaptchaWidget from '@/components/CaptchaWidget'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { BackToLogin, FormAlert } from '@/views/auth/parts'

export default function ForgotPasswordView() {
  const { t } = useTranslation(['auth'])

  const [methods, setMethods] = useState<AuthMethods | null>(null)
  const [ident, setIdent] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const [captcha, setCaptcha] = useState<LoginCaptcha>({})
  const [captchaRefresh, setCaptchaRefresh] = useState(0)
  const needCaptcha = !!methods?.captcha_forgot_required

  useEffect(() => { getAuthMethods().then(setMethods).catch(() => {}) }, [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!ident.trim() || busy) return
    setBusy(true)
    setError('')
    try {
      await requestPasswordReset(ident.trim(), captcha)
      setDone(true)
    } catch (err) {
      // A captcha failure is about the captcha, not account existence, so it's
      // safe to surface (and we must, or the user is silently stuck). Any other
      // error stays generic — the backend 200s for real requests (no enumeration).
      const e = err as { response?: { status?: number; data?: { captcha_required?: boolean } } }
      if (e.response?.data?.captcha_required) {
        setError(t('auth:captcha_required', { defaultValue: '请完成验证码' }))
        setCaptcha({})
        setCaptchaRefresh(x => x + 1)
      } else {
        setDone(true)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthLayout title={t('auth:forgot_password_title')} subtitle={t('auth:forgot_password_subtitle')}>
      {done ? (
        <FormAlert tone="success">{t('auth:forgot_password_success')}</FormAlert>
      ) : (
        <form onSubmit={submit} noValidate className="flex flex-col gap-4">
          <Field label={t('auth:forgot_password_upn_label')}>
            <Input name="username" value={ident} onChange={e => setIdent(e.target.value)} autoComplete="username" autoFocus />
          </Field>
          {needCaptcha && (
            <CaptchaWidget provider={methods?.captcha_provider ?? 'image'}
              siteKey={methods?.captcha_site_key} refreshKey={captchaRefresh} onChange={setCaptcha} />
          )}
          {error && <FormAlert tone="error">{error}</FormAlert>}
          <AsyncButton type="submit" size="lg" className="w-full" pending={busy} disabled={!ident.trim()}>
            {t('auth:forgot_password_submit')}
          </AsyncButton>
        </form>
      )}
      <BackToLogin />
    </AuthLayout>
  )
}
