import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import { IconIssue } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { AuthLayout } from '@/views/auth/AuthLayout'

export default function SsoNoAccountView() {
  const { t } = useTranslation('auth')
  const navigate = useNavigate()

  return (
    <AuthLayout icon={<IconIssue />} title={t('no_account_title')} subtitle={t('no_account_message')}>
      <Button size="lg" className="w-full" onClick={() => navigate('/login', { replace: true })}>
        {t('back_to_login')}
      </Button>
    </AuthLayout>
  )
}
