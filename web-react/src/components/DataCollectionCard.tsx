import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DataCollection, DataCollectionItem } from '@/api/legal'

// The order is the page's: what every account has first, the access-control
// records after. A key the server sends that is not listed has no sentence to
// say it in, so it is not drawn.
const ORDER = [
  'sub_log', 'auth_event', 'connection_history', 'hwid', 'flag_records', 'risk_signals', 'risk_reviews',
  'dest_hits', 'dest_trial', 'dest_usage',
] as const

/**
 * The "what we collect" list, generated from the settings the panel runs with,
 * so the page cannot drift from the retention it states. Every number comes from
 * the data (a retention of 0 is "kept for ever"); no text here states one.
 */
export default function DataCollectionCard({ data }: { data: DataCollection }) {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('auth')

  const retention = (days: number) => days === 0 ? t('legal.retention_forever') : t('legal.retention_days', { days })

  function value(key: string, item: DataCollectionItem): string {
    if (item.enabled_groups !== undefined) return t('legal.groups_enabled', { groups: item.enabled_groups })
    if (key === 'risk_signals') return t('legal.risk_signals_value')
    if (key === 'risk_reviews') return t('legal.risk_reviews_value')
    const parts: string[] = []
    if (item.nodes !== undefined) parts.push(t('legal.nodes', { count: item.nodes }))
    if (item.retention_days !== undefined) parts.push(retention(item.retention_days))
    return parts.length ? parts.join(' · ') : t('legal.recorded')
  }

  const rows = ORDER.filter(k => data[k]?.enabled)

  return (
    <Box component="section" aria-label={t('legal.collect_title')} sx={{
      my: 2.5, p: { xs: 2, sm: 2.5 }, borderRadius: 3, bgcolor: md.surfaceContainer, border: `1px solid ${md.outlineVariant}`,
    }}>
      <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, mb: 1, color: md.onSurface }}>{t('legal.collect_title')}</Typography>
      <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {rows.map(key => (
          <Box component="li" key={key} sx={{
            display: 'flex', flexDirection: { xs: 'column', sm: 'row' }, justifyContent: 'space-between',
            gap: { xs: 0, sm: 2 }, py: 0.75, borderTop: `1px solid ${md.outlineVariant}`, '&:first-of-type': { borderTop: 0 },
          }}>
            <Box component="span" sx={{ fontSize: 14, color: md.onSurface }}>{t(`legal.item.${key}`)}</Box>
            <Box component="span" sx={{ fontSize: 14, color: md.onSurfaceVariant, flexShrink: 0 }}>{value(key, data[key])}</Box>
          </Box>
        ))}
      </Box>
    </Box>
  )
}
