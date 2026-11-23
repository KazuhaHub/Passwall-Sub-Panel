import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { hasCustomFactor, milliToFactorText } from '@/utils/trafficFactor'

/**
 * The small "×1.5" marker next to a line or server whose traffic is priced off the default; renders
 * nothing at the default factor (1000). Same rules as the MUI TrafficFactorBadge (which the
 * self-service page still uses): factor 0 means the line is free. The words always carry the meaning,
 * so the free case reads "Free" rather than being told by colour.
 */
export function FactorBadge({ milli, className }: { milli: number | null | undefined; className?: string }) {
  const { t } = useTranslation('common')
  if (!hasCustomFactor(milli)) return null
  const text = milliToFactorText(milli)
  const free = milli === 0
  return (
    <Badge
      variant="outline"
      data-testid="traffic-factor-badge"
      className={cn('text-[11px]', className)}
      title={free ? t('traffic_factor.free_title') : t('traffic_factor.badge_title', { factor: text })}
    >
      {free ? t('traffic_factor.free') : `×${text}`}
    </Badge>
  )
}
