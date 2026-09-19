import { Box, Button, Typography, useTheme } from '@mui/material'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlined'
import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

// SSO_ERROR_KEYS maps the `error` code in the failure redirect onto the copy to
// render, and is the whole of what a failed sign-in says to the person looking at
// it.
//
// The Go handler owns the set (the page codes in auth_saml.go) and sends no
// description alongside it: a description would be reflected into this page, so
// anything an IdP chose to say would be rendered here and kept in browser history
// and Referer headers — and it would override the localized sentence. The full
// error goes to the process log and the classified reason to the metric, which is
// where an operator reads it.
//
// Exported so a test can assert that every code the backend can send has copy in
// both bundled languages.
export const SSO_ERROR_KEYS: Record<string, { title: string; message: string }> = {
  __default: { title: 'error_default_title', message: 'error_default_message' },
  auth_failed: { title: 'error_auth_failed_title', message: 'error_auth_failed_message' },
  // The IdP's response did not pass the panel's own checks — something an
  // administrator usually has to correct at the IdP.
  saml_config: { title: 'error_saml_config_title', message: 'error_saml_config_message' },
  // The replay-protection store is unreachable, so no sign-in can be completed.
  // Distinct from a refusal because the right response is to retry.
  saml_unavailable: { title: 'error_saml_unavailable_title', message: 'error_saml_unavailable_message' },
  account_disabled: { title: 'error_account_disabled_title', message: 'error_account_disabled_message' },
  account_pending: { title: 'error_account_pending_title', message: 'error_account_pending_message' },
  sso_conflict: { title: 'error_sso_conflict_title', message: 'error_sso_conflict_message' },
  sso_error: { title: 'error_sso_title', message: 'error_sso_message' },
}

export default function SsoErrorView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const [params] = useSearchParams()

  const error = params.get('error') || ''
  const { title: titleKey, message: messageKey } = SSO_ERROR_KEYS[error] ?? SSO_ERROR_KEYS.__default

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
          {t(messageKey)}
        </Typography>
        <Button variant="contained" onClick={() => navigate('/login', { replace: true })}>
          {t('back_to_login')}
        </Button>
      </Box>
    </Box>
  )
}
