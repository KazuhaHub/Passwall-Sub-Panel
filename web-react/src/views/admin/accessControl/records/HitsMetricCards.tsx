import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { Box } from '@mui/material'
import type { DestinationHitsPage } from '@/api/destinationHits'
import type { RecordsFilters } from './recordsParams'
import { useAccessTranslation } from '../useAccessTranslation'

export default function HitsMetricCards({ summary, action, onAction }: { summary?: DestinationHitsPage['summary']; action: RecordsFilters['action']; onAction: (value: RecordsFilters['action']) => void }) {
  const { t, number } = useAccessTranslation(['admin'])
  return <Box role="group" aria-label={t('admin:access_control.records.metrics')}><KpiGrid>{(['block', 'observe', 'users'] as const).map(key => <KpiTile key={key} label={t(`admin:access_control.records.${key}`)}
    value={summary ? number(summary[key]) : '—'} pressed={action === key}
    onToggle={key === 'users' ? undefined : () => onAction(action === key ? undefined : key)} />)}</KpiGrid></Box>
}
