import { useTranslation } from 'react-i18next'
import { StatusBadge } from '@/components/common/StatusBadge'
import { hasCustomFactor, milliToFactorText } from '@/utils/trafficFactor'

/** "×1.5" beside a line that costs more or less than its traffic; "Free" when it is not counted. Nothing at the default factor. */
export function FactorBadge({ milli }: { milli: number | null | undefined }) {
  const { t } = useTranslation('common')
  if (!hasCustomFactor(milli)) return null
  const text = milliToFactorText(milli)
  const free = milli === 0
  return (
    <span title={free ? t('traffic_factor.free_title') : t('traffic_factor.badge_title', { factor: text })}>
      <StatusBadge testId="traffic-factor-badge" tone={free ? 'success' : 'warning'} quiet={false} label={free ? t('traffic_factor.free') : `×${text}`} />
    </span>
  )
}
