import { Box, Button, Typography, useTheme } from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlined'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

export default function SsoErrorView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const [params] = useSearchParams()

  const error = params.get('error') || ''
  const description = params.get('description') || ''

  let titleKey = 'error_default_title'
  let messageKey = 'error_default_message'
  switch (error) {
    case 'auth_failed':
      titleKey = 'error_auth_failed_title'; messageKey = 'error_auth_failed_message'; break
    case 'account_disabled':
      titleKey = 'error_account_disabled_title'; messageKey = 'error_account_disabled_message'; break
    case 'account_pending':
      titleKey = 'error_account_pending_title'; messageKey = 'error_account_pending_message'; break
    case 'sso_conflict':
      titleKey = 'error_sso_conflict_title'; messageKey = 'error_sso_conflict_message'; break
    case 'sso_error':
      titleKey = 'error_sso_title'; messageKey = 'error_sso_message'; break
  }

  const Icon = error === 'account_disabled' ? WarningAmberIcon : ErrorOutlineIcon

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center', bgcolor: md.surface, p: 3 }}>
      <Box sx={{ textAlign: 'center', maxWidth: 520 }}>
        <Box sx={{
          width: 80, height: 80, borderRadius: '50%',
          display: 'grid', placeItems: 'center', mx: 'auto', mb: 2,
          bgcolor: md.errorContainer, color: md.onErrorContainer,
        }}>
          <Icon sx={{ fontSize: 40 }} />
        </Box>
        <Typography variant="h5" sx={{ fontWeight: 500, mb: 1 }}>{t(titleKey)}</Typography>
        <Typography variant="body2" sx={{ mb: 3, color: md.onSurfaceVariant }}>
          {description || t(messageKey)}
        </Typography>
        <Button variant="contained" onClick={() => navigate('/login', { replace: true })}>
          {t('back_to_login')}
        </Button>
      </Box>
    </Box>
  )
}
