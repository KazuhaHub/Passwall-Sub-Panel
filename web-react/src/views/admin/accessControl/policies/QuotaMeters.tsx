import { Alert, Box, LinearProgress, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { DestinationBudget } from '@/api/accessControl'
export function budgetExceeded(budget?: DestinationBudget): boolean { return !!budget && Object.values(budget).some(({ used, limit }) => used > limit) }
export default function QuotaMeters({ budget, compact = false }: { budget?: DestinationBudget; compact?: boolean }) {
  const { t } = useTranslation('admin')
  const keys = ['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'] as const
  return <Stack spacing={1}>
    <Box sx={{ display: 'grid', gridTemplateColumns: compact ? { xs: '1fr 1fr', md: 'repeat(4, 1fr)' } : '1fr', gap: 1.5 }}>
      {keys.filter(key => !['subjects', 'bytes'].includes(key) || !budget || budget[key].used >= budget[key].limit * .8).map(key => {
        const value = budget?.[key]; const ratio = value ? value.used / Math.max(1, value.limit) : 0
        return <Box key={key}><Typography variant="body2" color={ratio > 1 ? 'error' : ratio >= .8 ? 'warning.main' : 'text.secondary'}>
          {t(`admin:access_control.quota.${key}`)} {value ? `${value.used.toLocaleString()} / ${value.limit.toLocaleString()}` : '…'}
        </Typography>{!compact && <LinearProgress variant={value ? 'determinate' : 'indeterminate'} value={Math.min(100, ratio * 100)} color={ratio > 1 ? 'error' : ratio >= .8 ? 'warning' : 'primary'} />}</Box>
      })}
    </Box>
    {budgetExceeded(budget) && <Alert severity="error">{t('admin:access_control.quota.exceeded')}</Alert>}
  </Stack>
}
