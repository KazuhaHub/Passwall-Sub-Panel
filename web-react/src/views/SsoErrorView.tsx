import { useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { IconError, IconIssue } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { SSO_ERROR_KEYS } from '@/views/auth/ssoErrorKeys'

export default function SsoErrorView() {
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const [params] = useSearchParams()

  const error = params.get('error') || ''
  const { title: titleKey, message: messageKey } = SSO_ERROR_KEYS[error] ?? SSO_ERROR_KEYS.__default

  // A disabled account is a different kind of problem from a failed sign-in: the mark says so.
  const Icon = error === 'account_disabled' ? IconIssue : IconError

  return (
    <AuthLayout icon={<Icon />} title={t(titleKey)} subtitle={t(messageKey)}>
      <Button size="lg" className="w-full" onClick={() => navigate('/login', { replace: true })}>
        {t('back_to_login')}
      </Button>
    </AuthLayout>
  )
}
