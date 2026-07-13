import { useEffect, useState, type FormEvent } from 'react'
import { Alert, Box, Button, Card, CircularProgress, Link as MuiLink, TextField, Typography, useTheme } from '@mui/material'
import { Link as RouterLink, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { resetPassword } from '@/api/auth'
import { useSiteStore } from '@/stores/site'
import BrandLogo from '@/components/BrandLogo'

// A password meets the panel floor: >=8 chars with at least one letter and one
// digit (mirrors the backend's IsMinimallyStrongPassword).
function strongEnough(pw: string): boolean {
  return pw.length >= 8 && /[a-zA-Z]/.test(pw) && /[0-9]/.test(pw);
}

export default function ResetPasswordView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const site = useSiteStore()
  const [params] = useSearchParams()

  // A token in the URL → link delivery. Otherwise the user types their
  // username + the OTP code they received.
  const token = params.get('token') ?? ''
  const otpMode = token === ''

  const [ident, setIdent] = useState('')
  const [code, setCode] = useState('')
  const [pw, setPw] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => { void site.load() }, [site])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (pw !== confirm) { setError(t('auth:reset_password_mismatch')); return }
    if (!strongEnough(pw)) { setError(t('auth:password_too_weak', { defaultValue: '密码至少 8 位，且含字母和数字' })); return }
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
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface, px: 2 }}>
      <Card sx={{ width: '100%', maxWidth: 400, bgcolor: md.surfaceContainerLow, p: 4 }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', mb: 3 }}>
          <BrandLogo height={48} />
          <Typography variant="h5" sx={{ fontWeight: 500, mt: 1.5 }}>{t('auth:reset_password_title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5, color: md.onSurfaceVariant, textAlign: 'center' }}>
            {t('auth:reset_password_subtitle')}
          </Typography>
        </Box>

        {done ? (
          <>
            <Alert severity="success" sx={{ mb: 2 }}>{t('auth:reset_password_success')}</Alert>
            <Button variant="contained" fullWidth size="large" onClick={() => navigate('/login')}>
              {t('auth:logged_out_sign_in_again')}
            </Button>
          </>
        ) : (
          <Box component="form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {otpMode && (
              <>
                <TextField label={t('auth:forgot_password_upn_label')} value={ident}
                  onChange={e => setIdent(e.target.value)} autoComplete="username" fullWidth />
                <TextField label={t('auth:reset_password_code_label', { defaultValue: '验证码' })} value={code}
                  onChange={e => setCode(e.target.value)} fullWidth slotProps={{
                  htmlInput: { inputMode: 'numeric', maxLength: 8 }
                }} />
              </>
            )}
            <TextField label={t('auth:reset_password_new_label')} type="password" value={pw}
              onChange={e => setPw(e.target.value)} autoComplete="new-password" autoFocus={!otpMode} fullWidth />
            <TextField label={t('auth:reset_password_confirm_label')} type="password" value={confirm}
              onChange={e => setConfirm(e.target.value)} autoComplete="new-password" fullWidth />
            {error && <Alert severity="error" sx={{ py: 0 }}>{error}</Alert>}
            <Button type="submit" variant="contained" fullWidth size="large" disabled={busy}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('auth:reset_password_submit')}
            </Button>
          </Box>
        )}

        {!done && (
          <Box sx={{ mt: 2.5, textAlign: 'center' }}>
            <MuiLink component={RouterLink} to="/login" variant="body2"
              onClick={(e) => { e.preventDefault(); navigate('/login') }}>
              {t('auth:back_to_login')}
            </MuiLink>
          </Box>
        )}
      </Card>
    </Box>
  );
}
