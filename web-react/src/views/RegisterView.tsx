import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Alert, Box, Button, Card, Checkbox, CircularProgress, FormControlLabel, Link as MuiLink, TextField, Typography, useTheme } from '@mui/material'
import { Link as RouterLink, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { getAuthMethods, registerUser } from '@/api/auth'
import type { AuthMethods, LoginCaptcha } from '@/api/types'
import { useSiteStore } from '@/stores/site'
import BrandLogo from '@/components/BrandLogo'
import CaptchaWidget from '@/components/CaptchaWidget'
import LegalFooter from '@/components/LegalFooter'
import LegalLinks from '@/components/LegalLinks'

function strongEnough(pw: string): boolean {
  return pw.length >= 8 && /[a-zA-Z]/.test(pw) && /[0-9]/.test(pw)
}

export default function RegisterView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const site = useSiteStore()

  const [methods, setMethods] = useState<AuthMethods | null>(null)
  const [methodsError, setMethodsError] = useState(false)
  const [accepted, setAccepted] = useState(false)
  const [email, setEmail] = useState('')
  const [pw, setPw] = useState('')
  const [confirm, setConfirm] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [doneMsg, setDoneMsg] = useState('')
  const [captcha, setCaptcha] = useState<LoginCaptcha>({})
  const [captchaRefresh, setCaptchaRefresh] = useState(0)
  const needCaptcha = !!methods?.captcha_register_required
  const consentVersion = methods?.legal?.consent_version ?? 0
  const needLegal = !!methods?.legal?.enabled && consentVersion > 0

  const refreshMethods = useCallback(async () => {
    setMethods(null)
    setMethodsError(false)
    setAccepted(false)
    try { setMethods(await getAuthMethods()) }
    catch { setMethodsError(true) }
  }, [])

  useEffect(() => { void site.load() }, [site])
  useEffect(() => { void refreshMethods() }, [refreshMethods])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !methods) return
    setError('')
    if (needLegal && !accepted) { setError(t('auth:legal.required')); return }
    if (pw !== confirm) { setError(t('auth:reset_password_mismatch')); return }
    if (!strongEnough(pw)) { setError(t('auth:password_too_weak')); return }
    setBusy(true)
    try {
      const res = await registerUser({ email: email.trim(), password: pw, display_name: displayName.trim() || undefined, ...captcha,
        ...(needLegal ? { accepted_consent_version: consentVersion } : {}),
      })
      setDoneMsg(res.requires_verification ? t('auth:register_check_email') : t('auth:register_success'))
    } catch (err) {
      const e = err as AxiosError<{ error?: string; captcha_required?: boolean }>
      const status = e.response?.status
      const data = e.response?.data
      const msg = data?.error || ''
      if (status === 409 && msg === 'legal_consent_outdated') {
        setError(t('auth:legal.register_outdated'))
        await refreshMethods()
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
    <Box sx={{ minHeight: '100dvh', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', bgcolor: md.surface, px: 2, py: 3 }}>
      <Card sx={{ width: '100%', maxWidth: 400, bgcolor: md.surfaceContainerLow, p: { xs: 2, sm: 4 } }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', mb: 3 }}>
          <BrandLogo height={48} />
          <Typography variant="h5" sx={{ fontWeight: 500, mt: 1.5 }}>{t('auth:register_title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5, color: md.onSurfaceVariant }}>{t('auth:register_subtitle')}</Typography>
        </Box>

        {doneMsg ? (
          <>
            <Alert severity="success" sx={{ mb: 2 }}>{doneMsg}</Alert>
            <Button variant="contained" fullWidth size="large" onClick={() => navigate('/login')}>
              {t('auth:back_to_login')}
            </Button>
          </>
        ) : (
          <Box component="form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {error && <Alert severity="error" sx={{ py: 0 }}>{error}</Alert>}
            {methodsError && <Alert severity="error" action={<Button onClick={() => void refreshMethods()} disabled={busy} sx={{ minHeight: 44 }}>{t('auth:legal.retry')}</Button>}>{t('auth:legal.register_settings_failed')}</Alert>}
            <TextField label={t('auth:register_email_label')} type="email" value={email}
              onChange={e => setEmail(e.target.value)} autoComplete="email" autoFocus fullWidth />
            <TextField label={t('auth:register_display_name_label')} value={displayName}
              onChange={e => setDisplayName(e.target.value)} fullWidth />
            <TextField label={t('auth:register_password_label')} type="password" value={pw}
              onChange={e => setPw(e.target.value)} autoComplete="new-password" fullWidth />
            <TextField label={t('auth:register_password_confirm_label')} type="password" value={confirm}
              onChange={e => setConfirm(e.target.value)} autoComplete="new-password" fullWidth />
            {needCaptcha && (
              <CaptchaWidget provider={methods?.captcha_provider ?? 'image'}
                siteKey={methods?.captcha_site_key} refreshKey={captchaRefresh} onChange={setCaptcha} />
            )}
            {needLegal && <FormControlLabel sx={{ alignItems: 'flex-start', m: 0 }} control={<Checkbox checked={accepted} onChange={event => setAccepted(event.target.checked)}
              disabled={busy} slotProps={{ input: { 'aria-label': t('auth:legal.checkbox_label') } }} sx={{ minWidth: 44, minHeight: 44 }} />}
              label={<Box component="span" sx={{ fontSize: 14, lineHeight: 1.75 }}>{t('auth:legal.agree_prefix')} <LegalLinks newTab version={consentVersion} /></Box>} />}
            {methods?.registration_require_email_verification && (
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                {t('auth:register_verify_hint')}
              </Typography>
            )}
            <Button type="submit" variant="contained" fullWidth size="large" disabled={busy || !methods || !email.trim() || !pw || (needLegal && !accepted)}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('auth:register_submit')}
            </Button>
          </Box>
        )}

        {!doneMsg && (
          <Box sx={{ mt: 2.5, textAlign: 'center' }}>
            <MuiLink component={RouterLink} to="/login" variant="body2"
              onClick={(e) => { e.preventDefault(); navigate('/login') }}>
              {t('auth:back_to_login')}
            </MuiLink>
          </Box>
        )}
      </Card>
      <LegalFooter text={site.footerText} enabled={methods?.legal?.enabled ?? site.legalEnabled} />
    </Box>
  )
}
