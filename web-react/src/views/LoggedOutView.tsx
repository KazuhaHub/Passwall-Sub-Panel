import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import { IconLogout } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { AuthLayout } from '@/views/auth/AuthLayout'

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
  const { t } = useTranslation('auth')
  const navigate = useNavigate()

  return (
    <AuthLayout icon={<IconLogout />} title={t('logged_out_title')} subtitle={t('logged_out_message')}>
      <Button size="lg" className="w-full" onClick={() => navigate('/login', { replace: true })}>
        {t('logged_out_sign_in_again')}
      </Button>
    </AuthLayout>
  )
}
