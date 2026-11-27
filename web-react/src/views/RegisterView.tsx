import { Fragment, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link as RouterLink, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { getAuthMethods, registerUser } from '@/api/auth'
import type { AuthMethods, LoginCaptcha } from '@/api/types'
import CaptchaWidget from '@/components/CaptchaWidget'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { PasswordField } from '@/views/auth/PasswordField'
import { BackToLogin, FormAlert, focusFirstInvalid } from '@/views/auth/parts'

function strongEnough(pw: string): boolean {
  return pw.length >= 8 && /[a-zA-Z]/.test(pw) && /[0-9]/.test(pw)
}

export default function RegisterView() {
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const form = useRef<HTMLFormElement>(null)

  const [methods, setMethods] = useState<AuthMethods | null>(null)
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [confirm, setConfirm] = useState('')
  const [displayName, setDisplayName] = useState('')
  // A password problem is shown once the box has been left (or on submit), not while it
  // is still being typed.
  const [pwSeen, setPwSeen] = useState(false)
  const [confirmSeen, setConfirmSeen] = useState(false)
  // Set by the first submit: an empty box is only reported as missing once the person has tried to go on.
  const [submitted, setSubmitted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [doneMsg, setDoneMsg] = useState('')
  const [captcha, setCaptcha] = useState<LoginCaptcha>({})
  const [captchaRefresh, setCaptchaRefresh] = useState(0)
  const needCaptcha = !!methods?.captcha_register_required
  // The terms are asked for only while a document can really be opened; the
  // version offered is the one the form was loaded with.
  const consentVersion = methods?.legal?.enabled ? methods.legal.consent_version : null
  const [agreed, setAgreed] = useState(false)

  useEffect(() => { getAuthMethods().then(setMethods).catch(() => {}) }, [])

  const pwError = pw
    ? (pwSeen && !strongEnough(pw) ? t('auth:password_too_weak') : undefined)
    : (submitted ? t('auth:password_required') : undefined)
  const confirmError = confirm
    ? (confirmSeen && pw !== confirm ? t('auth:reset_password_mismatch') : undefined)
    : (submitted ? t('auth:password_confirm_required') : undefined)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setPwSeen(true)
    setConfirmSeen(true)
    setSubmitted(true)
    if (!pw || !confirm || pw !== confirm || !strongEnough(pw)) {
      // The messages are drawn from the two flags set above; this only has to stop and focus.
      focusFirstInvalid(form.current)
      return
    }
    setBusy(true)
    try {
      const res = await registerUser({ email: email.trim(), password: pw, display_name: displayName.trim() || undefined, ...captcha,
        ...(consentVersion !== null ? { accepted_consent_version: consentVersion } : {}),
      })
      setDoneMsg(res.requires_verification ? t('auth:register_check_email') : t('auth:register_success'))
    } catch (err) {
      const e = err as AxiosError<{ error?: string; code?: string; captcha_required?: boolean }>
      const status = e.response?.status
      const data = e.response?.data
      const msg = data?.error || ''
      if (status === 409 && data?.code === 'legal_consent_outdated') {
        // The terms moved while the form was open: read them again, on the
        // current version, before the box can be ticked.
        setError(t('auth:legal.register_outdated'))
        setAgreed(false)
        getAuthMethods().then(setMethods).catch(() => {})
      } else if (data?.captcha_required) {
        setError(t('auth:captcha_required', { defaultValue: '请完成验证码' }))
        setCaptcha({})
        setCaptchaRefresh(x => x + 1)
      } else if (status === 409 || /exist/i.test(msg)) setError(t('auth:register_email_exists'))
      else if (/domain/i.test(msg)) setError(t('auth:register_email_domain_not_allowed'))
      else if (status === 400 && /email/i.test(msg)) setError(t('auth:register_invalid_email', { defaultValue: '邮箱格式不正确' }))
      else setError(t('auth:register_error'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthLayout title={t('auth:register_title')} subtitle={t('auth:register_subtitle')}>
      {doneMsg ? (
        <>
          <FormAlert tone="success">{doneMsg}</FormAlert>
          <Button size="lg" className="w-full" onClick={() => navigate('/login')}>
            {t('auth:back_to_login')}
          </Button>
        </>
      ) : (
        <>
          <form ref={form} onSubmit={submit} noValidate className="flex flex-col gap-4">
            <Field label={t('auth:register_email_label')}>
              <Input type="email" name="email" value={email} onChange={e => setEmail(e.target.value)}
                autoComplete="email" autoFocus />
            </Field>
            <Field label={t('auth:register_display_name_label')}>
              <Input name="name" value={displayName} onChange={e => setDisplayName(e.target.value)} autoComplete="name" />
            </Field>
            <Field label={t('auth:register_password_label')} error={pwError}>
              <PasswordField name="password" value={pw} onChange={e => setPw(e.target.value)}
                onBlur={() => setPwSeen(true)} autoComplete="new-password" />
            </Field>
            <Field label={t('auth:register_password_confirm_label')} error={confirmError}>
              <PasswordField name="password_confirm" value={confirm} onChange={e => setConfirm(e.target.value)}
                onBlur={() => setConfirmSeen(true)} autoComplete="new-password" />
            </Field>
            {needCaptcha && (
              <CaptchaWidget provider={methods?.captcha_provider ?? 'image'}
                siteKey={methods?.captcha_site_key} refreshKey={captchaRefresh} onChange={setCaptcha} />
            )}
            {consentVersion !== null && (
              <label className="flex items-start gap-2.5 text-sm">
                <Checkbox checked={agreed} onCheckedChange={c => setAgreed(c)} className="mt-0.5" />
                <span>
                  {t('auth:legal.register_agree_prefix')}
                  {(['terms', 'privacy'] as const).map((kind, i) => (
                    <Fragment key={kind}>
                      {i > 0 && t('auth:legal.register_agree_and')}
                      {/* A link inside the label does not tick the box; it opens
                          the document in another tab so the form is kept. */}
                      <RouterLink to={`/legal/${kind}`} target="_blank" rel="noopener noreferrer"
                        className="text-primary underline underline-offset-4">
                        {t(`auth:legal.register_${kind}_link`)}
                      </RouterLink>
                    </Fragment>
                  ))}
                  {t('auth:legal.register_agree_suffix')}
                </span>
              </label>
            )}
            {error && <FormAlert tone="error">{error}</FormAlert>}
            {methods?.registration_require_email_verification && (
              <p className="text-xs text-muted-foreground">{t('auth:register_verify_hint')}</p>
            )}
            <AsyncButton type="submit" size="lg" className="w-full" pending={busy}
              disabled={!email.trim() || !pw || (consentVersion !== null && !agreed)}>
              {t('auth:register_submit')}
            </AsyncButton>
          </form>
          <BackToLogin />
        </>
      )}
    </AuthLayout>
  )
}
