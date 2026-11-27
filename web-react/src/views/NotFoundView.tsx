import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { IconSearch } from '@/lib/icons'

/**
 * An unknown address inside the app shell: say so, and offer the way back. (A typo in /admin/... used to bounce to the
 * dashboard without a word, which hid the typo.) `home` is the role's start page.
 */
export default function NotFoundView({ home }: { home: string }) {
  const { t } = useTranslation('common')
  return (
    <div className="p-4 sm:p-6">
      <EmptyState icon={<IconSearch aria-hidden />} title={t('ui.shell.not_found_title')} description={t('ui.shell.not_found_body')}
        action={<Button variant="outline" render={<Link to={home} />}>{t('ui.shell.back_home')}</Button>} />
    </div>
  )
}
