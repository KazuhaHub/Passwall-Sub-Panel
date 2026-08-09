import { useEffect, useState } from 'react'
import { Box, CircularProgress, Typography, useTheme } from '@mui/material'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth'

export default function SsoCallbackView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('auth')
  const auth = useAuthStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [error, setError] = useState<string>('')

  useEffect(() => {
    let cancelled = false
    async function run() {
      try {
        await auth.loginSSO()
        if (cancelled) return
        navigate(params.get('next') || '/user/me', { replace: true })
      } catch (e) {
        if (cancelled) return
        const msg = (e as { response?: { data?: { error?: string } } }).response?.data?.error
          ?? t('sso_callback_failed')
        setError(msg)
      }
    }
    void run()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface }}>
      {error
        ? <Typography sx={{ color: md.error, fontSize: 14 }}>{error}</Typography>
        : (
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2 }}>
            <CircularProgress />
            <Typography variant="body2">{t('sso_callback_progress')}</Typography>
          </Box>
        )}
    </Box>
  )
}
