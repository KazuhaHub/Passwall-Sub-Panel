import { Box, Typography, useTheme } from '@mui/material'
import type { StageGroupTime } from '@/utils/diagnostics'
import type { StageGroup } from '@/utils/diagnosticsCatalog'
import type { DiagFormat } from './useDiagFormat'

/**
 * Where an average poll's time goes: one bar, a segment per group in poll
 * order, each as wide as its share, with a legend that wraps on a phone
 * rather than squeezing labels into slivers.
 */
export default function StageBar({ groups, fmt }: { groups: StageGroupTime[]; fmt: DiagFormat }) {
  const md = useTheme().palette.md
  const color: Record<StageGroup, string> = {
    db: md.secondary,
    panels: md.primary,
    compute: md.tertiary,
    write: md.outline,
    geo: md.error,
  }
  const shown = groups.filter(g => g.avgMs > 0)
  if (shown.length === 0) return null
  const { t } = fmt
  return (
    <Box sx={{ mt: 1.5 }} data-testid="stage-bar">
      <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant, mb: 0.5 }}>
        {t('admin:diagnostics.cards.poll.stages.title')}
      </Typography>
      <Box sx={{ display: 'flex', height: 10, borderRadius: 1, overflow: 'hidden', bgcolor: md.surfaceContainerHighest }}>
        {shown.map(g => (
          <Box key={g.group} sx={{ flexGrow: g.avgMs, flexBasis: 0, bgcolor: color[g.group], minWidth: 2 }} />
        ))}
      </Box>
      <Box component="ul" sx={{ listStyle: 'none', p: 0, m: 0, mt: 0.75, display: 'flex', flexWrap: 'wrap', columnGap: 2, rowGap: 0.5 }}>
        {shown.map(g => (
          <Box component="li" key={g.group} sx={{ display: 'flex', alignItems: 'center', gap: 0.75, minWidth: 0 }}>
            <Box aria-hidden sx={{ width: 10, height: 10, borderRadius: '2px', bgcolor: color[g.group], flex: '0 0 auto' }} />
            <Typography variant="caption" sx={{ fontVariantNumeric: 'tabular-nums' }}>
              {`${t(`admin:diagnostics.cards.poll.stages.${g.group}`)} ${fmt.latency(g.avgMs)}`}
            </Typography>
          </Box>
        ))}
      </Box>
      <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant, mt: 0.5 }}>
        {t('admin:diagnostics.cards.poll.stages.note')}
      </Typography>
    </Box>
  )
}
