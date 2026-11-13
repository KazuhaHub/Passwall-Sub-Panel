import { Box, Button, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestBudget } from '@/api/accessControl'
import { amber } from '@/components/ToneBadge'
import { anyBudgetOver, budgetLevel, visibleBudget, type BudgetLevel } from '@/utils/accessControl'

const Q = 'admin:access_control.policies.quota.'

function useLevelColor() {
  const theme = useTheme()
  const md = theme.palette.md
  return (level: BudgetLevel, fallback: string) => level === 'over' ? md.error : level === 'near' ? amber(theme).fg : fallback
}

/**
 * The wire quotas the policies use. Compact: one line above the rail (the
 * four always shown, subjects and bytes only from 80%). Full: the editor's
 * bars for the budget as it would be after saving. At 80% the figure is amber,
 * over 100% it is the error colour and says why that matters: a new version
 * over the quota is not applied, nodes keep the previous one.
 */
export default function QuotaMeters({ budget, variant, loading = false, failed = false, onRetry, suffix }: {
  budget: DestBudget | undefined
  variant: 'compact' | 'full'
  loading?: boolean
  failed?: boolean
  onRetry?: () => void
  /** Compact only: a quiet closing note ("no node enforces right now"). */
  suffix?: string
}) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const colorFor = useLevelColor()
  const num = new Intl.NumberFormat(i18n.language)

  if (failed) {
    return (
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
        {t(`${Q}failed`)} {onRetry && <Button size="small" onClick={onRetry}>{t('admin:access_control.retry')}</Button>}
      </Typography>
    )
  }
  const kinds = budget ? visibleBudget(budget) : (['rules', 'domains', 'regexps', 'cidrs'] as (keyof DestBudget)[])
  const over = anyBudgetOver(budget)

  if (variant === 'compact') {
    return (
      <Box>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>
          {t(`${Q}label`)}{'  '}
          {kinds.map((k, i) => {
            const item = budget?.[k]
            return (
              <Box key={k} component="span">
                {i > 0 && ' · '}
                {t(`${Q}kind.${k}`)}{' '}
                <Box component="span" sx={{ color: item ? colorFor(budgetLevel(item), md.onSurface) : md.onSurface, fontWeight: budgetLevel(item) === 'ok' ? 400 : 600 }}>
                  {item ? `${num.format(item.used)} / ${num.format(item.limit)}` : '…'}
                </Box>
              </Box>
            )
          })}
          {suffix ? ` · ${suffix}` : ''}
        </Typography>
        {over && <Typography variant="caption" sx={{ color: md.error, display: 'block' }}>{t(`${Q}over_note`)}</Typography>}
      </Box>
    )
  }

  return (
    <Box>
      <Typography sx={{ fontSize: 14, fontWeight: 600, mb: 0.5 }}>{t(`${Q}after_save`)}</Typography>
      {kinds.map(k => {
        const item = budget?.[k]
        const level = budgetLevel(item)
        const pct = item && item.limit > 0 ? Math.min(100, (item.used / item.limit) * 100) : 0
        return (
          <Box key={k} sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '72px minmax(0, 1fr) 120px' }, columnGap: 1.5, alignItems: 'center', mb: 0.5 }}>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${Q}kind.${k}`)}</Typography>
            <Box role="progressbar" aria-valuemin={0} aria-valuemax={item?.limit ?? 0} aria-valuenow={item?.used ?? 0}
              aria-label={t(`${Q}kind.${k}`)} sx={{ height: 4, borderRadius: 2, bgcolor: md.surfaceContainerHighest, overflow: 'hidden' }}>
              <Box sx={{ width: `${pct}%`, height: '100%', bgcolor: colorFor(level, md.primary) }} />
            </Box>
            <Typography variant="body2" sx={{ textAlign: { sm: 'right' }, fontVariantNumeric: 'tabular-nums', color: colorFor(level, md.onSurface) }}>
              {loading || !item ? (failed ? '—' : '…') : `${num.format(item.used)} / ${num.format(item.limit)}`}
            </Typography>
          </Box>
        )
      })}
      {over && <Typography variant="caption" sx={{ color: md.error }}>{t(`${Q}over_note`)}</Typography>}
    </Box>
  )
}
