import { Box, Button, Typography, useTheme } from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

export default function SsoNoAccountView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('auth')
  const navigate = useNavigate()

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface, p: 3 }}>
      <Box sx={{ textAlign: 'center', maxWidth: 520 }}>
        <Box sx={{
          width: 80, height: 80, borderRadius: '50%',
          display: 'grid', placeItems: 'center', mx: 'auto', mb: 2,
          bgcolor: md.errorContainer, color: md.onErrorContainer,
        }}>
          <WarningAmberIcon sx={{ fontSize: 40 }} />
        </Box>
        <Typography variant="h5" sx={{ fontWeight: 500, mb: 1 }}>{t('no_account_title')}</Typography>
        <Typography variant="body2" sx={{ mb: 3, color: md.onSurfaceVariant }}>
          {t('no_account_message')}
        </Typography>
        <Button variant="contained" onClick={() => navigate('/login', { replace: true })}>
          {t('back_to_login')}
        </Button>
      </Box>
    </Box>
  )
}
