import { useEffect, useState, type FormEvent } from 'react'
import { Alert, Box, Button, Card, CircularProgress, Link as MuiLink, TextField, Typography, useTheme } from '@mui/material'
import ArrowBackIcon from '@mui/icons-material/ArrowBack'
import { Link as RouterLink, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

import { requestPasswordReset } from '@/api/auth'
import { useSiteStore } from '@/stores/site'
import BrandLogo from '@/components/BrandLogo'

export default function ForgotPasswordView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth'])
  const navigate = useNavigate()
  const site = useSiteStore()

  const [ident, setIdent] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  useEffect(() => { void site.load() }, [site])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!ident.trim() || busy) return
    setBusy(true)
    try {
      await requestPasswordReset(ident.trim())
    } catch {
      // The backend always 200s (no enumeration); a transport error still
      // shouldn't reveal anything, so we show the same success state.
    } finally {
      setBusy(false)
      setDone(true)
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
