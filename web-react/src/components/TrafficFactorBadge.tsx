import { Chip } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { hasCustomFactor, milliToFactorText } from '@/utils/trafficFactor'

/** Small "×1.5" chip; renders nothing at the default factor (1000). */
export function TrafficFactorBadge({ milli, sx }: { milli: number | null | undefined; sx?: object }) {
  const { t } = useTranslation('common')
  if (!hasCustomFactor(milli)) return null
  const text = milliToFactorText(milli)
  const free = milli === 0
  return (
    <Chip
      size="small"
      variant="outlined"
      color={free ? 'success' : 'warning'}
      data-testid="traffic-factor-badge"
      label={free ? t('traffic_factor.free', { defaultValue: '不计流量' }) : `×${text}`}
      title={free
        ? t('traffic_factor.free_title', { defaultValue: '此服务器的流量不计入配额' })
        : t('traffic_factor.badge_title', { factor: text, defaultValue: '流量倍率 ×{{factor}}' })}
      sx={{ height: 20, fontSize: 11, '& .MuiChip-label': { px: 0.75 }, ...sx }}
    />
  )
}
