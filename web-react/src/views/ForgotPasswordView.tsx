import { useEffect, useState, type FormEvent } from 'react'
import { Alert, Box, Button, Card, CircularProgress, Link as MuiLink, TextField, Typography, useTheme } from '@mui/material'
import ArrowBackIcon from '@mui/icons-material/ArrowBack'
import { Link as RouterLink, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

import { getAuthMethods, requestPasswordReset } from '@/api/auth'
import type { AuthMethods, LoginCaptcha } from '@/api/types'
import { useSiteStore } from '@/stores/site'
import BrandLogo from '@/components/BrandLogo'
import CaptchaWidget from '@/components/CaptchaWidget'

export default function ForgotPasswordView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const site = useSiteStore()

  const [methods, setMethods] = useState<AuthMethods | null>(null)
  const [ident, setIdent] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const [captcha, setCaptcha] = useState<LoginCaptcha>({})
  const [captchaRefresh, setCaptchaRefresh] = useState(0)
  const needCaptcha = !!methods?.captcha_forgot_required

  useEffect(() => { void site.load() }, [site])
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
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface, px: 2 }}>
      <Card sx={{ width: '100%', maxWidth: 400, bgcolor: md.surfaceContainerLow, p: 4 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', mb: 3 }}>
          <BrandLogo height={48} />
          <Typography variant="h5" sx={{ fontWeight: 500, mt: 1.5 }}>{t('auth:forgot_password_title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5, color: md.onSurfaceVariant, textAlign: 'center' }}>
            {t('auth:forgot_password_subtitle')}
          </Typography>
        </Box>

        {done ? (
          <Alert severity="success" sx={{ mb: 1 }}>{t('auth:forgot_password_success')}</Alert>
        ) : (
          <Box component="form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <TextField label={t('auth:forgot_password_upn_label')} value={ident}
              onChange={e => setIdent(e.target.value)} autoComplete="username" autoFocus fullWidth />
            {needCaptcha && (
              <CaptchaWidget provider={methods?.captcha_provider ?? 'image'}
                siteKey={methods?.captcha_site_key} refreshKey={captchaRefresh} onChange={setCaptcha} />
            )}
            {error && <Alert severity="error" sx={{ py: 0 }}>{error}</Alert>}
            <Button type="submit" variant="contained" fullWidth size="large" disabled={busy || !ident.trim()}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('auth:forgot_password_submit')}
            </Button>
          </Box>
        )}

        <Box sx={{ mt: 2.5, textAlign: 'center' }}>
          <MuiLink component={RouterLink} to="/login" variant="body2"
            sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}
            onClick={(e) => { e.preventDefault(); navigate('/login') }}>
            <ArrowBackIcon sx={{ fontSize: 16 }} />{t('auth:back_to_login')}
          </MuiLink>
        </Box>
      </Card>
    </Box>
  )
}
