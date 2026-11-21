import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { resendVerification, verifyEmail } from '@/api/auth'
import { IconSpinner } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { BackToLogin, FormAlert } from '@/views/auth/parts'

export default function VerifyEmailView() {
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const [params] = useSearchParams()

  const token = params.get('token') ?? ''
  const otpMode = token === ''

  const [ident, setIdent] = useState('')
  const [code, setCode] = useState('')
  const [state, setState] = useState<'idle' | 'verifying' | 'done' | 'error'>('idle')
  const [error, setError] = useState('')
  const autoTried = useRef(false)

  // Resend: a 60s client cooldown mirrors the server-side throttle so the
  // button reflects when another send would actually go out.
  const [resend, setResend] = useState<'idle' | 'sending' | 'sent'>('idle')
  const [resendLeft, setResendLeft] = useState(0)

  useEffect(() => {
    if (resendLeft <= 0) return
    const id = setInterval(() => setResendLeft(s => Math.max(0, s - 1)), 1000)
    return () => clearInterval(id)
  }, [resendLeft])

  async function doResend() {
    if (!ident.trim() || resendLeft > 0 || resend === 'sending') return
    setResend('sending')
    setError('')
    try {
      await resendVerification({ email: ident.trim() })
      setResend('sent')
      setResendLeft(60)
    } catch (err) {
      const status = (err as AxiosError).response?.status
      // 400 here means captcha is required for registration — the widget lives
      // on the register page, so steer the user back there to trigger a resend.
      setError(status === 400 ? t('auth:verify_email_resend_need_captcha') : t('auth:verify_email_error'))
      setResend('idle')
    }
  }

  async function run(input: { token?: string; ident?: string; code?: string }) {
    setState('verifying')
    setError('')
    try {
      await verifyEmail(input)
      setState('done')
    } catch (err) {
      const status = (err as AxiosError).response?.status
      setError(status === 401 ? t('auth:verify_email_invalid_token') : t('auth:verify_email_error'))
      setState('error')
    }
  }

  // Link mode: verify automatically on load.
  useEffect(() => {
    if (!otpMode && !autoTried.current) {
      autoTried.current = true
      void run({ token })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function submitOtp(e: FormEvent) {
    e.preventDefault()
    if (!ident.trim() || !code.trim()) return
    void run({ ident: ident.trim(), code: code.trim() })
  }

  return (
    <AuthLayout title={t('auth:verify_email_title')} subtitle={t('auth:verify_email_subtitle')}>
      {state === 'done' ? (
        <>
          <FormAlert tone="success">{t('auth:verify_email_success')}</FormAlert>
          <Button size="lg" className="w-full" onClick={() => navigate('/login')}>
            {t('auth:logged_out_sign_in_again')}
          </Button>
        </>
      ) : !otpMode ? (
        <>
          {state === 'verifying' && (
            <div role="status" className="grid place-items-center py-2">
              <IconSpinner className="size-6 animate-spin text-muted-foreground" />
              <span className="sr-only">{t('auth:verify_email_title')}</span>
            </div>
          )}
          {state === 'error' && <FormAlert tone="error">{error}</FormAlert>}
          <BackToLogin />
        </>
      ) : (
        <>
          <form onSubmit={submitOtp} noValidate className="flex flex-col gap-4">
            <Field label={t('auth:register_email_label')}>
              <Input type="email" name="email" value={ident} onChange={e => setIdent(e.target.value)} autoComplete="email" autoFocus />
            </Field>
            <Field label={t('auth:verify_email_code_label')}>
              <Input name="code" value={code} onChange={e => setCode(e.target.value)}
                autoComplete="one-time-code" inputMode="numeric" maxLength={8} />
            </Field>
            {error && <FormAlert tone="error">{error}</FormAlert>}
            <AsyncButton type="submit" size="lg" className="w-full" pending={state === 'verifying'}>
              {t('auth:verify_email_submit')}
            </AsyncButton>
          </form>
          <div className="flex flex-col items-center gap-1 text-center">
            {resend === 'sent' && <p className="text-sm text-muted-foreground">{t('auth:verify_email_resent')}</p>}
            <Button variant="ghost" size="sm" onClick={() => void doResend()}
              disabled={!ident.trim() || resendLeft > 0 || resend === 'sending'}>
              {resendLeft > 0
                ? t('auth:verify_email_resend_in', { sec: resendLeft })
                : t('auth:verify_email_resend')}
            </Button>
          </div>
          <BackToLogin />
        </>
      )}
    </AuthLayout>
  )
}
