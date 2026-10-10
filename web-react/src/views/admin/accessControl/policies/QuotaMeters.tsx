import { Alert, Box, LinearProgress, Stack, Typography } from '@mui/material'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import type { DestinationBudget } from '@/api/accessControl'
export function budgetExceeded(budget?: DestinationBudget): boolean { return !!budget && Object.values(budget).some(({ used, limit }) => used > limit) }
export default function QuotaMeters({ budget, compact = false, unavailable = false }: { budget?: DestinationBudget; compact?: boolean; unavailable?: boolean }) {
  const { t, number } = useAccessTranslation('admin')
  const shownBudget = unavailable ? undefined : budget
  const keys = ['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'] as const
  return <Stack spacing={1}>
    <Box sx={{ display: 'grid', gridTemplateColumns: compact ? { xs: '1fr 1fr', md: 'repeat(4, 1fr)' } : '1fr', gap: 1.5 }}>
      {keys.filter(key => !['subjects', 'bytes'].includes(key) || !shownBudget || shownBudget[key].used >= shownBudget[key].limit * .8).map(key => {
        const value = shownBudget?.[key]; const ratio = value ? value.used / Math.max(1, value.limit) : 0
        return <Box key={key} sx={{ display: 'flex', flexDirection: 'column', gap: .5 }}><Typography variant="body2" sx={{ order: compact ? 0 : { xs: 1, sm: 0 } }} color={ratio > 1 ? 'error' : ratio >= .8 ? 'warning.main' : 'text.secondary'}>
          {t(`admin:access_control.quota.${key}`)} {value ? `${number(value.used)} / ${number(value.limit)}` : unavailable ? '—' : '…'}
        </Typography>{!compact && (unavailable ? <Box aria-hidden sx={{ height: 4, borderRadius: 1, bgcolor: 'action.disabledBackground', order: { xs: 0, sm: 1 } }} /> : <LinearProgress aria-label={t(`admin:access_control.quota.${key}`)} aria-valuetext={value ? `${number(value.used)} / ${number(value.limit)}` : undefined} sx={{ height: 4, borderRadius: 1, order: { xs: 0, sm: 1 } }} variant={value ? 'determinate' : 'indeterminate'} value={Math.min(100, ratio * 100)} color={ratio > 1 ? 'error' : ratio >= .8 ? 'warning' : 'primary'} />)}</Box>
      })}
    </Box>
    {budgetExceeded(shownBudget) && <Alert severity="error">{t('admin:access_control.quota.exceeded')}</Alert>}
  </Stack>
}
