import { Box, Button, Typography, useTheme } from '@mui/material'
import LogoutIcon from '@mui/icons-material/Logout'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

// LoggedOutView is the landing page after a successful logout. The
// reason this exists at all: when login_mode = sso_redirect the
// /login route immediately bounces back to the IdP, and since the
// IdP usually keeps its own session cookie, the user is logged
// straight back in. /logged-out is a static intermediate that doesn't
// probe auth methods and doesn't auto-redirect, so the logout
// actually "sticks" until the user clicks Sign in again. Also
// matches the typical "You have been signed out" page enterprise
// SSO portals show.
export default function LoggedOutView() {
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
          bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
        }}>
          <LogoutIcon sx={{ fontSize: 40 }} />
        </Box>
        <Typography variant="h5" sx={{ fontWeight: 500, mb: 1 }}>
          {t('logged_out_title')}
        </Typography>
        <Typography variant="body2" sx={{ mb: 3, color: md.onSurfaceVariant }}>
          {t('logged_out_message')}
        </Typography>
        <Button variant="contained" onClick={() => navigate('/login', { replace: true })}>
          {t('logged_out_sign_in_again')}
        </Button>
      </Box>
    </Box>
  )
}
