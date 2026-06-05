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
import { Link as RouterLink, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { getAuthMethods, oidcLoginURL, samlLoginURL } from '@/api/auth'
import type { AuthMethods, LoginCaptcha } from '@/api/types'
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

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!upn || !password) {
      pushSnack(t('auth:missing_credentials'), 'warning')
      return
    }
    setBusy(true)
    setLockedMsg('')
    try {
      await auth.login(upn, password, showCaptcha ? captcha : undefined)
      const fallback = homeForRole(useAuthStore.getState().role)
      const requested = returnTo ?? fallback
      const isAdmin = selectIsAdmin(useAuthStore.getState())
      const target = isAdminPath(requested) && !isAdmin ? fallback : requested
      navigate(target, { replace: true })
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
      <Button type="submit" variant={ssoFirst ? 'outlined' : 'contained'} fullWidth size="large"
        disabled={busy}
        startIcon={busy ? <CircularProgress size={16} color="inherit" /> : <LoginIcon />}
        sx={{ mt: 1 }}>
        {t('auth:submit')}
      </Button>
    </Box>
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

          {ssoFirst ? (
            <>{ssoBlock}{dividerBlock}{localFormBlock}</>
          ) : (
            <>{localFormBlock}{dividerBlock}{ssoBlock}</>
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
