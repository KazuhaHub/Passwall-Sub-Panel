import { useEffect, useState, type FormEvent } from 'react'
import {
  Alert,
  Box,
  Button,
  Card,
  CircularProgress,
  Divider,
  Link as MuiLink,
  TextField,
  Typography,
  useTheme,
} from '@mui/material'
import LoginIcon from '@mui/icons-material/Login'
import OpenInNewIcon from '@mui/icons-material/OpenInNew'
import FingerprintIcon from '@mui/icons-material/Fingerprint'
import { Link as RouterLink, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { getAuthMethods, oidcLoginURL, samlLoginURL, send2FAEmail } from '@/api/auth'
import type { AuthMethods, LoginCaptcha, TwoFAMethod } from '@/api/types'
import CaptchaWidget from '@/components/CaptchaWidget'
import { useAuthStore, selectIsAdmin } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { useAppearanceStore } from '@/stores/appearance'
import { homeForRole, isAdminPath } from '@/router/home'
import { pushSnack } from '@/components/SnackbarHost'
import LanguageMenu from '@/components/LanguageMenu'
import AppearanceMenu from '@/components/AppearanceMenu'
import BrandLogo from '@/components/BrandLogo'
import { setLanguage, currentLanguage } from '@/i18n'
import { DEFAULT_PRESET_HEX, type AppLanguage } from '@/theme'

interface LocationState {
  returnTo?: string
}

export default function LoginView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth', 'common'])
  const navigate = useNavigate()
  const location = useLocation()

  const auth = useAuthStore()
  const site = useSiteStore()
  const appearance = useAppearanceStore()

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
  const [twoFAMode, setTwoFAMode] = useState<'totp' | 'recovery' | 'email'>('totp')
  // Set when the user clicks the SSO button in sso_first / dual mode. We
  // render the same "正在前往 SSO" intermediate as sso_redirect so every
  // SSO entry point (auto-bounce OR explicit click) feels the same.
  const [manualSsoRedirect, setManualSsoRedirect] = useState(false)

  useEffect(() => { void site.load() }, [site])
  useEffect(() => {
    // getState() avoids subscribing — putting `appearance` in deps causes
    // an infinite loop (setSystemColor → store updates → effect re-fires).
    if (site.loaded) useAppearanceStore.getState().setSystemColor(site.themeColor || DEFAULT_PRESET_HEX)
  }, [site.loaded, site.themeColor])

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
        if (m.login_mode === 'sso_redirect' && m.sso) {
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
  }, [returnTo])

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
        setTwoFAMode('totp')
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
    setPassword('')
  }

  // Switch the code-entry method (totp ⇄ recovery). Email needs a send first.
  function switchTwoFAMode(mode: 'totp' | 'recovery') {
    setTwoFAMode(mode)
    setTwoFACode('')
    setTwoFAError('')
  }

  // onUseEmail switches to the email step and requests a one-time code.
  async function onUseEmail() {
    if (!twoFAPending) return
    setTwoFAMode('email')
    setTwoFACode('')
    setTwoFAError('')
    setBusy(true)
    try {
      await send2FAEmail(twoFAPending)
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
        setTwoFAMode('totp')
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

  const localEnabled = methods?.local !== false  // default to true if probe failed
  const samlEnabled = !!methods?.saml
  const oidcEnabled = !!methods?.oidc
  // sso_first: SSO buttons render BEFORE the local form so admins who picked
  // this mode get the prominent placement they wanted. dual / local_only
  // keep the original local-form-first layout.
  const ssoFirst = methods?.login_mode === 'sso_first'
  const isRedirecting = (methods?.login_mode === 'sso_redirect' && methods?.sso) || manualSsoRedirect

  if (probing) {
    return (
      <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface }}>
        <CircularProgress />
      </Box>
    )
  }

  if (isRedirecting) {
    return (
      <Box sx={{
        position: 'fixed', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 2.5, bgcolor: md.surface,
      }}>
        <BrandLogo height={56} />
        <CircularProgress size={28} />
        <Typography sx={{ fontSize: 15, fontWeight: 500, color: md.onSurface }}>
          {t('auth:redirect_pending_title')}
        </Typography>
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
          {t('auth:redirect_pending_body')}
        </Typography>
      </Box>
    )
  }

  const localFormBlock = localEnabled && (
    <Box component="form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <TextField label={t('auth:username')} value={upn}
        onChange={e => setUpn(e.target.value)}
        autoComplete="username" autoFocus fullWidth />
      <TextField label={t('auth:password')} type="password" value={password}
        onChange={e => setPassword(e.target.value)}
        autoComplete="current-password" fullWidth />
      {showCaptcha && methods?.captcha_enabled && (
        <CaptchaWidget
          provider={methods.captcha_provider ?? 'image'}
          siteKey={methods.captcha_site_key}
          refreshKey={captchaRefresh}
          onChange={setCaptcha}
        />
      )}
      {lockedMsg && <Alert severity="warning" sx={{ py: 0 }}>{lockedMsg}</Alert>}
      {methods?.password_recovery_enabled && (
        <Box sx={{ textAlign: 'right', mt: -0.5 }}>
          <MuiLink component={RouterLink} to="/forgot-password" variant="body2"
            onClick={(e) => { e.preventDefault(); navigate('/forgot-password') }}>
            {t('auth:forgot_password')}
          </MuiLink>
        </Box>
      )}
      {methods?.registration_enabled && (
        <Box sx={{ textAlign: 'center', mt: 0.5, fontSize: 13, color: md.onSurfaceVariant }}>
          {t('auth:register_prompt')}{' '}
          <MuiLink component={RouterLink} to="/register" variant="body2"
            onClick={(e) => { e.preventDefault(); navigate('/register') }}>
            {t('auth:create_account')}
          </MuiLink>
        </Box>
      )}
      <Button type="submit" variant={ssoFirst ? 'outlined' : 'contained'} fullWidth size="large"
        disabled={busy}
        startIcon={busy ? <CircularProgress size={16} color="inherit" /> : <LoginIcon />}
        sx={{ mt: 1 }}>
        {t('auth:submit')}
      </Button>
    </Box>
  )

  // Per-method copy for the code field. Passkey has no code field (it's an
  // immediate ceremony), so it's only an alternative-method button.
  const twoFAModeUI = {
    totp: { prompt: t('auth:twofa_prompt'), label: t('auth:twofa_code'), placeholder: '123456' },
    recovery: { prompt: t('auth:twofa_recovery_prompt'), label: t('auth:twofa_recovery_label'), placeholder: 'XXXXX-XXXXX' },
    email: { prompt: t('auth:twofa_email_prompt'), label: t('auth:twofa_email_label'), placeholder: '123456' },
  }[twoFAMode]

  // Alternative-method buttons, excluding whatever is currently active.
  const altButtons: React.ReactNode[] = []
  if (twoFAMode !== 'totp') {
    altButtons.push(<Button key="totp" size="small" onClick={() => switchTwoFAMode('totp')} disabled={busy}>{t('auth:twofa_use_totp')}</Button>)
  }
  if (twoFAMethods.includes('recovery') && twoFAMode !== 'recovery') {
    altButtons.push(<Button key="recovery" size="small" onClick={() => switchTwoFAMode('recovery')} disabled={busy}>{t('auth:twofa_use_recovery')}</Button>)
  }
  if (twoFAMethods.includes('email') && twoFAMode !== 'email') {
    altButtons.push(<Button key="email" size="small" onClick={onUseEmail} disabled={busy}>{t('auth:twofa_use_email')}</Button>)
  }
  if (twoFAMethods.includes('passkey')) {
    altButtons.push(<Button key="passkey" size="small" startIcon={<FingerprintIcon />} onClick={onUsePasskey2FA} disabled={busy}>{t('auth:twofa_use_passkey')}</Button>)
  }

  const twoFAFormBlock = (
    <Box component="form" onSubmit={submit2FA} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
        {twoFAModeUI.prompt}
      </Typography>
      <TextField
        label={twoFAModeUI.label}
        value={twoFACode}
        onChange={e => setTwoFACode(e.target.value)}
        autoFocus
        fullWidth
        autoComplete="one-time-code"
        inputProps={{ inputMode: 'text', autoCapitalize: 'characters' }}
        placeholder={twoFAModeUI.placeholder}
      />
      {twoFAError && <Alert severity="error" sx={{ py: 0 }}>{twoFAError}</Alert>}
      <Button type="submit" variant="contained" fullWidth size="large"
        disabled={busy}
        startIcon={busy ? <CircularProgress size={16} color="inherit" /> : <LoginIcon />}
        sx={{ mt: 1 }}>
        {t('auth:twofa_submit')}
      </Button>
      {altButtons.length > 0 && (
        <Box sx={{ mt: 0.5 }}>
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant, display: 'block', textAlign: 'center', mb: 0.5 }}>
            {t('auth:twofa_use_another')}
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, justifyContent: 'center' }}>
            {altButtons}
          </Box>
        </Box>
      )}
      <Button variant="text" fullWidth size="small" onClick={cancel2FA} disabled={busy}>
        {t('auth:twofa_back')}
      </Button>
    </Box>
  )

  const passkeyBlock = localEnabled && methods?.passkey_passwordless && (
    <Button type="button" variant="outlined" fullWidth size="large"
      onClick={onPasskeyLogin} disabled={busy}
      startIcon={<FingerprintIcon />}
      sx={{ mt: 1.5 }}>
      {t('auth:passkey_login')}
    </Button>
  )

  const ssoBlock = (samlEnabled || oidcEnabled) && (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      {samlEnabled && (
        <Button variant={ssoFirst ? 'contained' : 'outlined'} fullWidth size="large"
          onClick={() => ssoLogin('saml')} startIcon={<OpenInNewIcon />}>
          {t(oidcEnabled ? 'auth:saml_login' : 'auth:sso_login')}
        </Button>
      )}
      {oidcEnabled && (
        <Button variant={ssoFirst && !samlEnabled ? 'contained' : 'outlined'} fullWidth size="large"
          onClick={() => ssoLogin('oidc')} startIcon={<OpenInNewIcon />}>
          {t(samlEnabled ? 'auth:oidc_login' : 'auth:sso_login')}
        </Button>
      )}
    </Box>
  )

  const dividerBlock = localEnabled && (samlEnabled || oidcEnabled) && (
    <Divider sx={{ my: 3, color: md.onSurfaceVariant, fontSize: 12 }}>
      {t('auth:or')}
    </Divider>
  )

  return (
    <Box sx={{
      position: 'fixed', inset: 0,
      display: 'flex', flexDirection: 'column',
      bgcolor: md.surface,
    }}>
      {/* Compact toolbar with language + appearance only — no nav, no avatar */}
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', p: 1.5, gap: 0.5 }}>
        <LanguageMenu value={currentLanguage()} onChange={(l: AppLanguage) => setLanguage(l)} />
        <AppearanceMenu
          state={{ systemColor: appearance.systemColor, userColor: appearance.userColor, mode: appearance.mode }}
          onChange={(patch) => {
            if ('userColor' in patch) appearance.setUserColor(patch.userColor ?? null)
            if (patch.mode) appearance.setMode(patch.mode)
          }}
        />
      </Box>

      <Box sx={{ flex: 1, display: 'grid', placeItems: 'center', px: 2 }}>
        <Card sx={{
          width: '100%', maxWidth: 400,
          bgcolor: md.surfaceContainerLow,
          boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)',
          p: 4,
        }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', mb: 3 }}>
            <BrandLogo height={56} />
            <Typography variant="h5" sx={{ fontWeight: 500, color: md.onSurface, mt: 1.5 }}>
              {site.appTitle || site.siteTitle}
            </Typography>
            <Typography variant="body2" sx={{ mt: 0.5 }}>{t('auth:subtitle')}</Typography>
          </Box>

          {twoFAPending ? (
            twoFAFormBlock
          ) : ssoFirst ? (
            <>{ssoBlock}{dividerBlock}{localFormBlock}{passkeyBlock}</>
          ) : (
            <>{localFormBlock}{passkeyBlock}{dividerBlock}{ssoBlock}</>
          )}
        </Card>
      </Box>

      {site.footerText && (
        <Box sx={{ p: 2, textAlign: 'center', fontSize: 12, color: md.onSurfaceVariant }}>
          {site.footerText}
        </Box>
      )}
    </Box>
  )
}
