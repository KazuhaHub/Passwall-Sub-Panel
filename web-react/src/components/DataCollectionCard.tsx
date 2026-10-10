import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { useId } from 'react'
import type { DataCollection } from '@/api/legal'

export default function DataCollectionCard({ data }: { data: DataCollection }) {
  const { t } = useTranslation('auth')
  const titleId = useId()
  const md = useTheme().palette.md
  const retention = (days: number) => days === 0 ? t('legal.forever') : t('legal.retention_days', { days })
  const rows: { key: string; label: string; value: string }[] = [
    { key: 'sub', label: t('legal.collection_sub'), value: retention(data.sub_log_retention_days) },
    { key: 'auth', label: t('legal.collection_auth'), value: retention(data.auth_event_retention_days) },
    { key: 'connections', label: t('legal.collection_connections'), value: retention(data.connection_retention_days) },
    ...(data.hwid_captured ? [{ key: 'hwid', label: t('legal.collection_hwid'), value: retention(data.hwid_retention_days) }] : []),
    { key: 'flags', label: t('legal.collection_flags'), value: retention(data.flag_record_retention_days) },
    { key: 'assessment', label: t('legal.collection_assessment'), value: t('legal.refresh_minutes', { minutes: data.risk_assessment_refresh_minutes }) },
    { key: 'reviews', label: t('legal.collection_reviews'), value: t('legal.review_retention', { minutes: data.risk_review_purge_after_deletion_minutes }) },
    ...data.access.filter(item => item.nodes > 0).map(item => ({
      key: item.kind, label: t(`legal.collection_${item.kind}`, { count: item.nodes }), value: retention(item.retention_days),
    })),
  ]
  return <Box component="section" aria-labelledby={titleId} sx={{ my: 3, p: { xs: 2, sm: 3 }, borderRadius: 3, bgcolor: md.surfaceContainer, color: md.onSurface }}>
    <Typography id={titleId} component="h2" variant="h6" sx={{ fontSize: 18, mb: 2 }}>{t('legal.collection_title')}</Typography>
    <Box component="dl" sx={{ m: 0 }}>
      {rows.map(row => <Box key={row.key} sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'minmax(0, 1fr) minmax(0, 1fr)' }, gap: 0.5, py: 1, '&:not(:last-child)': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
        <Typography component="dt" variant="body2">{row.label}</Typography>
        <Typography component="dd" variant="body2" sx={{ m: 0, color: md.onSurfaceVariant, textAlign: { xs: 'left', sm: 'right' } }}>{row.value}</Typography>
      </Box>)}
    </Box>
  </Box>
}
