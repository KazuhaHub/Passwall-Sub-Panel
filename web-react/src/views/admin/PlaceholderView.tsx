import { useLocation } from 'react-router'
import { useTranslation } from 'react-i18next'
import { IconConstruction } from '@/lib/icons'
import { EmptyState } from '@/components/feedback/EmptyState'

// Stands in for any admin route that has no page yet (router PLACEHOLDER_PATHS).
export default function PlaceholderView() {
  const location = useLocation()
  const { t } = useTranslation('common')
  return (
    <div className="p-4 sm:p-6">
      <EmptyState
        icon={<IconConstruction />}
        title={t('placeholder.title')}
        description={`${t('placeholder.body')} ${location.pathname}`}
      />
    </div>
  )
}
