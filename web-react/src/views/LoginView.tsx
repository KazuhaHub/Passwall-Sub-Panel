import { useEffect, useState, type FormEvent } from 'react'
import { Link as RouterLink, useLocation, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { getAuthMethods, oidcLoginURL, samlLoginURL, send2FAEmail } from '@/api/auth'
import type { AuthMethods, LoginCaptcha, TwoFAMethod } from '@/api/types'
import { TWOFA_PRIORITY, defaultTwoFAMethod, writeLast2FAMethod } from '@/utils/twofa'
import { IconExternalLink, IconFingerprint, IconLogin, IconSpinner } from '@/lib/icons'
import CaptchaWidget from '@/components/CaptchaWidget'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAuthStore, selectIsAdmin } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { homeForRole, isAdminPath } from '@/router/home'
import { useSiteBootstrap } from '@/router/shellBootstrap'
import { pushSnack } from '@/components/SnackbarHost'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { PasswordField } from '@/views/auth/PasswordField'
import { TwoFactorStep } from '@/views/auth/TwoFactorStep'
import { FormAlert, OrDivider } from '@/views/auth/parts'

interface LocationState {
  returnTo?: string
}

// The 2FA challenge defaults to the method the user chose last time (per-browser),
// with the rest tucked behind "use another method" — so a returning user lands
// straight on their usual factor instead of re-picking every login. The
// method-selection logic is shared with the in-app step-up dialogs — see
// @/utils/twofa (defaultTwoFAMethod / writeLast2FAMethod).

// forceLocal renders the local-login entry (/login/local): the local form +
// passkey button only, never auto-redirecting to SSO and never showing SSO
// buttons. It reuses all of LoginView's machinery (passkey, 2FA challenge,
// captcha) so the admin break-glass entry stays in sync with the main page.
export default function LoginView({ forceLocal = false }: { forceLocal?: boolean } = {}) {
  const { t } = useTranslation(['auth', 'common'])
  const navigate = useNavigate()
  const location = useLocation()

  const auth = useAuthStore()
  const appTitle = useSiteStore(st => st.appTitle)
  const siteTitle = useSiteStore(st => st.siteTitle)

  const [methods, setMethods] = useState<AuthMethods | null>(null)
  const [probing, setProbing] = useState(true)
  const [upn, setUpn] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  // Captcha: shown upfront in "always" mode, or once a failed login returns
  // captcha_required (after_failures mode). refreshKey forces a new challenge
  // after each failed attempt (the prior image is single-use / consumed).
  const [showCaptcha, setShowCaptcha] = useState(false)
  const [captcha, setCaptcha] = useState<LoginCaptcha>({})
  const [captchaRefresh, setCaptchaRefresh] = useState(0)
  const [lockedMsg, setLockedMsg] = useState('')
  // 2FA challenge: set to the pending token once a 2FA-enabled account passes the
  // password step. While set, the card renders the code-entry step instead.
  const [twoFAPending, setTwoFAPending] = useState<string | null>(null)
  const [twoFACode, setTwoFACode] = useState('')
  const [twoFAError, setTwoFAError] = useState('')
  // The verification methods the server accepts for this challenge, and which
  // code-entry method the user currently has selected.
  const [twoFAMethods, setTwoFAMethods] = useState<TwoFAMethod[]>([])
  const [twoFAMode, setTwoFAMode] = useState<TwoFAMethod>('totp')
  // The challenge opens on the last-used method; the chooser stays hidden behind
  // "use another method" until the user wants to switch.
  const [showMethodPicker, setShowMethodPicker] = useState(false)
  // Email is the one method that needs a code dispatched first — track whether
  // we've sent one so the email view shows "send code" vs the code input.
  const [emailSent, setEmailSent] = useState(false)
  // Email-code resend cooldown: seconds remaining before a resend is allowed.
  // Mirrors the server-side per-account throttle; the duration comes from the
  // /methods config (twofa_email_resend_cooldown_sec) so admins can tune it. This
  // is what stops "可以一直发" — the button is disabled with a live countdown.
  const [emailCd, setEmailCd] = useState(0)
  useEffect(() => {
    if (emailCd <= 0) return
    const id = setTimeout(() => setEmailCd(s => s - 1), 1000)
    return () => clearTimeout(id)
  }, [emailCd])
  // Set when the user clicks the SSO button in sso_first / dual mode. We
  // render the same "正在前往 SSO" intermediate as sso_redirect so every
  // SSO entry point (auto-bounce OR explicit click) feels the same.
  const [manualSsoRedirect, setManualSsoRedirect] = useState(false)

  // Branding is loaded here as well as in AuthLayout so it is fetched while the methods are
  // being probed, not after: the layout only mounts once the probe has answered.
  useSiteBootstrap()

  const returnTo =
    (location.state as LocationState | null)?.returnTo
    ?? new URLSearchParams(location.search).get('return_to')
    ?? undefined

  useEffect(() => {
    let cancelled = false
    async function probe() {
      try {
        const m = await getAuthMethods()
        if (cancelled) return
        setMethods(m)
        // "always" mode demands a captcha before the first attempt.
        if (m.captcha_enabled && m.captcha_required) setShowCaptcha(true)
        if (!forceLocal && m.login_mode === 'sso_redirect' && m.sso) {
          // Show the redirect-pending screen (rendered when methods is set
          // and login_mode === sso_redirect). 3s gives the user enough time
          // to register what's happening and read the message before the
          // browser hands off to the IdP.
          setProbing(false)
          setTimeout(() => {
            if (cancelled) return
            window.location.href = m.saml ? samlLoginURL(returnTo ?? '/user/me') : oidcLoginURL(returnTo ?? '/user/me')
          }, 3000)
          return
        }
      } catch {
        // probe failure leaves methods null and we render the local form
      } finally {
        if (!cancelled) setProbing(false)
      }
    }
    void probe()
    return () => { cancelled = true }
  }, [returnTo, forceLocal])

  // goHome routes to the requested page (or the role's default), guarding the
  // admin area against non-admins. Shared by the password and 2FA submit paths.
  function goHome() {
    const fallback = homeForRole(useAuthStore.getState().role)
    const requested = returnTo ?? fallback
    const isAdmin = selectIsAdmin(useAuthStore.getState())
    const target = isAdminPath(requested) && !isAdmin ? fallback : requested
    navigate(target, { replace: true })
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!upn || !password) {
      pushSnack(t('auth:missing_credentials'), 'warning')
      return
    }
    setBusy(true)
    setLockedMsg('')
    try {
      const outcome = await auth.login(upn, password, showCaptcha ? captcha : undefined)
      if (outcome.twoFA) {
        // Password OK, but the account needs a second factor: switch to the
        // code-entry step rather than navigating.
        setTwoFAPending(outcome.pendingToken)
        setTwoFAMethods(outcome.methods)
        setTwoFAMode(defaultTwoFAMethod(outcome.methods))
        setShowMethodPicker(false)
        setEmailSent(false)
        setTwoFACode('')
        setTwoFAError('')
        return
      }
      goHome()
    } catch (err) {
      // The axios interceptor already toasted the message; here we react to the
      // structured flags to drive the inline captcha / lockout UI.
      const e = err as AxiosError<{ captcha_required?: boolean; locked?: boolean; retry_after?: number }>
      const data = e.response?.data
      if (e.response?.status === 429 || data?.locked) {
        const mins = Math.max(1, Math.ceil((data?.retry_after ?? 60) / 60))
        setLockedMsg(t('auth:locked', { minutes: mins }))
      }
      if (data?.captcha_required) setShowCaptcha(true)
      // Refresh the (now-consumed or stale) challenge for the retry.
      if (showCaptcha || data?.captcha_required) {
        setCaptcha({})
        setCaptchaRefresh(x => x + 1)
      }
    } finally {
      setBusy(false)
    }
  }

  async function submit2FA(e: FormEvent) {
    e.preventDefault()
    if (!twoFAPending) return
    const code = twoFACode.trim()
    if (!code) {
      setTwoFAError(t('auth:twofa_code_required'))
      return
    }
    setBusy(true)
    setTwoFAError('')
    try {
      await auth.complete2FA(twoFAPending, code)
      // Don't make the one-time recovery fallback the sticky default; remember
      // only "real" methods (totp / email) the user would want to land on again.
      if (twoFAMode !== 'recovery') writeLast2FAMethod(twoFAMode)
      goHome()
    } catch (err) {
      // A wrong/expired code: the verify endpoint skips the shared toast, so
      // surface it inline. An expired pending token (5-min TTL) needs a full
      // re-login — send the user back to the password step.
      const e = err as AxiosError<{ error?: string }>
      if (e.response?.status === 401 && /session/i.test(e.response?.data?.error ?? '')) {
        cancel2FA()
        pushSnack(t('auth:twofa_session_expired'), 'warning')
      } else {
        setTwoFAError(t('auth:twofa_invalid'))
        setTwoFACode('')
      }
    } finally {
      setBusy(false)
    }
  }

  function cancel2FA() {
    setTwoFAPending(null)
    setTwoFACode('')
    setTwoFAError('')
    setTwoFAMethods([])
    setTwoFAMode('totp')
    setShowMethodPicker(false)
    setEmailSent(false)
    setEmailCd(0)
    setPassword('')
  }

  // selectMethod is the 2FA method picker: email needs a code sent first; passkey
  // just switches to its button; totp/recovery switch the code field. It collapses
  // the chooser back down after a pick.
  function selectMethod(m: TwoFAMethod) {
    setShowMethodPicker(false)
    if (m === 'email') {
      // Switch to the email step; only auto-send the FIRST time. Re-selecting
      // email later just shows the code input (resend is the explicit button,
      // gated by the cooldown) — so picking the method can't re-spam mail.
      setTwoFAMode('email')
      if (!emailSent) void onUseEmail()
      return
    }
    setTwoFAMode(m)
    setTwoFACode('')
    setTwoFAError('')
  }

  // onUseEmail switches to the email step and requests a one-time code. Sets
  // emailSent so the view flips from "send code" to the code input.
  async function onUseEmail() {
    if (!twoFAPending || emailCd > 0) return
    setTwoFAMode('email')
    setTwoFACode('')
    setTwoFAError('')
    setBusy(true)
    try {
      await send2FAEmail(twoFAPending)
      setEmailSent(true)
      setEmailCd(methods?.twofa_email_resend_cooldown_sec || 60)
      pushSnack(t('auth:twofa_email_sent'), 'success')
    } catch {
      setTwoFAError(t('auth:twofa_email_failed'))
    } finally {
      setBusy(false)
    }
  }

  // onUsePasskey2FA completes the challenge by asserting a passkey (no code).
  async function onUsePasskey2FA() {
    if (!twoFAPending) return
    setBusy(true)
    setTwoFAError('')
    try {
      await auth.complete2FAPasskey(twoFAPending)
      writeLast2FAMethod('passkey')
      goHome()
    } catch (err) {
      const e = err as AxiosError<{ error?: string }>
      const name = (err as { name?: string })?.name
      if (name === 'NotAllowedError' || name === 'AbortError') {
        // user dismissed the browser prompt — no error
      } else if (e.response?.status === 401 && /session/i.test(e.response?.data?.error ?? '')) {
        cancel2FA()
        pushSnack(t('auth:twofa_session_expired'), 'warning')
      } else {
        setTwoFAError(t('auth:passkey_failed'))
      }
    } finally {
      setBusy(false)
    }
  }

  async function onPasskeyLogin() {
    setBusy(true)
    setLockedMsg('')
    try {
      const outcome = await auth.loginPasskey()
      if (outcome.twoFA) {
        setTwoFAPending(outcome.pendingToken)
        setTwoFAMethods(outcome.methods)
        setTwoFAMode(defaultTwoFAMethod(outcome.methods))
        setShowMethodPicker(false)
        setEmailSent(false)
        setTwoFACode('')
        setTwoFAError('')
        return
      }
      goHome()
    } catch (err) {
      // A user who cancels the browser prompt (or has no matching passkey)
      // shouldn't see a scary error; only surface genuine failures.
      const name = (err as { name?: string })?.name
      if (name !== 'NotAllowedError' && name !== 'AbortError') {
        pushSnack(t('auth:passkey_failed'), 'error')
      }
    } finally {
      setBusy(false)
    }
  }

  function ssoLogin(provider: 'saml' | 'oidc') {
    const url = provider === 'saml'
      ? samlLoginURL(returnTo ?? '/user/me')
      : oidcLoginURL(returnTo ?? '/user/me')
    setManualSsoRedirect(true)
    // Brief intermediate so the user sees what's happening before the
    // browser hands off to the IdP. 3s matches the sso_redirect path.
    setTimeout(() => { window.location.href = url }, 3000)
  }

  // forceLocal (the /login/local entry) forces the local form on and hides SSO.
  const localEnabled = forceLocal || methods?.local !== false  // default to true if probe failed
  const samlEnabled = !forceLocal && !!methods?.saml
  const oidcEnabled = !forceLocal && !!methods?.oidc
  // sso_first: SSO buttons render BEFORE the local form so admins who picked
  // this mode get the prominent placement they wanted. dual / local_only
  // keep the original local-form-first layout.
  const ssoFirst = !forceLocal && methods?.login_mode === 'sso_first'
  const isRedirecting = !forceLocal && ((methods?.login_mode === 'sso_redirect' && methods?.sso) || manualSsoRedirect)


  // 2FA verification methods are ordered by TWOFA_PRIORITY (passkey first — phishing-resistant)
  // and filtered to what the server allows for this challenge.
  const methodOrder = TWOFA_PRIORITY.filter(m => twoFAMethods.includes(m))
  const title = appTitle || siteTitle

  if (probing) {
    return (
      <div role="status" className="grid min-h-svh place-items-center bg-background">
        <IconSpinner aria-hidden className="size-6 animate-spin text-muted-foreground" />
        <span className="sr-only">{t('common:ui.feedback.loading')}</span>
      </div>
    )
  }

  if (isRedirecting) {
    return (
      <AuthLayout title={t('auth:redirect_pending_title')} subtitle={t('auth:redirect_pending_body')}>
        <div role="status" className="grid place-items-center">
          <IconSpinner aria-hidden className="size-7 animate-spin text-muted-foreground" />
        </div>
      </AuthLayout>
    )
  }

  const localFormBlock = localEnabled && (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4">
      <Field label={t('auth:username')}>
        <Input name="username" value={upn} onChange={e => setUpn(e.target.value)} autoComplete="username" autoFocus />
      </Field>
      {/* The recovery link is the field's hint line: under the box, on the right, where the
          eye is when the password has just failed. */}
      <Field label={t('auth:password')} hint={methods?.password_recovery_enabled && (
        <span className="block text-right">
          <RouterLink to="/forgot-password"
            className="inline-flex min-h-11 items-center text-muted-foreground underline-offset-4 hover:text-foreground hover:underline sm:min-h-0">
            {t('auth:forgot_password')}
          </RouterLink>
        </span>
      )}>
        <PasswordField name="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="current-password" />
      </Field>
      {showCaptcha && methods?.captcha_enabled && (
        <CaptchaWidget
          provider={methods.captcha_provider ?? 'image'}
          siteKey={methods.captcha_site_key}
          refreshKey={captchaRefresh}
          onChange={setCaptcha}
        />
      )}
      {lockedMsg && <FormAlert tone="warning">{lockedMsg}</FormAlert>}
      <AsyncButton type="submit" variant={ssoFirst ? 'outline' : 'default'} size="lg" className="w-full"
        pending={busy} icon={<IconLogin />}>
        {t('auth:submit')}
      </AsyncButton>
    </form>
  )

  const passkeyBlock = localEnabled && methods?.passkey_passwordless && (
    <Button type="button" variant="outline" size="lg" className="w-full" onClick={onPasskeyLogin} disabled={busy}>
      <IconFingerprint />
      {t('auth:passkey_login')}
    </Button>
  )

  const ssoBlock = (samlEnabled || oidcEnabled) && (
    <div className="flex flex-col gap-2">
      {samlEnabled && (
        <Button variant={ssoFirst ? 'default' : 'outline'} size="lg" className="w-full" onClick={() => ssoLogin('saml')}>
          <IconExternalLink />
          {t(oidcEnabled ? 'auth:saml_login' : 'auth:sso_login')}
        </Button>
      )}
      {oidcEnabled && (
        <Button variant={ssoFirst && !samlEnabled ? 'default' : 'outline'} size="lg" className="w-full" onClick={() => ssoLogin('oidc')}>
          <IconExternalLink />
          {t(samlEnabled ? 'auth:oidc_login' : 'auth:sso_login')}
        </Button>
      )}
    </div>
  )

  const dividerBlock = localEnabled && (samlEnabled || oidcEnabled) && <OrDivider />

  // Local sign-in and passkey stay together; the SSO buttons go before or after them
  // by the admin's login mode (sso_first puts SSO first).
  const localGroup = (localFormBlock || passkeyBlock) && (
    <div className="flex flex-col gap-3">{localFormBlock}{passkeyBlock}</div>
  )

  return (
    <AuthLayout title={title} subtitle={t(forceLocal ? 'auth:local_only_subtitle' : 'auth:subtitle')}>
      {twoFAPending ? (
        <TwoFactorStep
          mode={twoFAMode}
          methodOrder={methodOrder}
          code={twoFACode}
          onCodeChange={setTwoFACode}
          error={twoFAError}
          busy={busy}
          emailSent={emailSent}
          emailCd={emailCd}
          showPicker={showMethodPicker}
          onShowPicker={() => setShowMethodPicker(true)}
          onSubmitCode={submit2FA}
          onUsePasskey={onUsePasskey2FA}
          onSendEmail={() => void onUseEmail()}
          onSelectMethod={selectMethod}
          onBack={cancel2FA}
        />
      ) : (
        <>
          {ssoFirst
            ? <>{ssoBlock}{dividerBlock}{localGroup}</>
            : <>{localGroup}{dividerBlock}{ssoBlock}</>}
          {localEnabled && methods?.registration_enabled && (
            <p className="text-center text-sm text-muted-foreground">
              {t('auth:register_prompt')}{' '}
              <RouterLink to="/register" className="text-primary underline-offset-4 hover:underline">
                {t('auth:create_account')}
              </RouterLink>
            </p>
          )}
        </>
      )}
    </AuthLayout>
  )
}
