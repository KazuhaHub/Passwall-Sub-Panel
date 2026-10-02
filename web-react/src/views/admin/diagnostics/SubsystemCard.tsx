import type { ReactNode } from 'react'
import { Box, Button, Card, Collapse, Typography, useTheme } from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { Link as RouterLink } from 'react-router'
import HelpTip from '@/components/HelpTip'
import { copyToClipboard } from '@/utils/clipboard'
import type { CardSummary, RawFamily } from '@/utils/diagnostics'
import { CARD_LINK, LINK_TARGET } from '@/utils/diagnosticsCatalog'
import RawMetricTable from './RawMetricTable'
import { CardStateBadge } from './StatusBadge'
import type { DiagFormat } from './useDiagFormat'

/** The series a card's families hold in this reading, as the API sent them. */
export function cardSeries(families: RawFamily[]) {
  const series = families.flatMap(f => f.series)
  return {
    counters: series.flatMap(s => s.counter ? [s.counter] : []),
    gauges: series.flatMap(s => s.gauge ? [s.gauge] : []),
    histograms: series.flatMap(s => s.histogram ? [s.histogram] : []),
  }
}

/**
 * One area of the page: its title, state badge and purpose, one sentence in
 * plain words, the body the area defines, and a footer with the page to go
 * to and its own slice of the raw data. The id is the anchor a finding's
 * "show data" scrolls to.
 */
export default function SubsystemCard({ card, sentence, extra, children, families, windowMs, fmt, expanded, onToggle, wide }: {
  card: CardSummary
  sentence: string
  /** Lines under the sentence that hold whatever the body shows (notices). */
  extra?: ReactNode
  /** The figures; left out by the caller whenever they must be withheld. */
  children?: ReactNode
  /** This card's families in the current reading (rawFamilies). */
  families: RawFamily[]
  windowMs: number
  fmt: DiagFormat
  expanded: boolean
  onToggle: () => void
  wide: boolean
}) {
  const md = useTheme().palette.md
  const { t } = fmt
  const link = CARD_LINK[card.id]
  const target = link ? LINK_TARGET[link] : undefined
  const seriesCount = families.reduce((n, f) => n + f.series.length, 0)
  const copyGroup = () => void copyToClipboard(JSON.stringify({ card: card.id, ...cardSeries(families) }, null, 2))

  return (
    <Card id={`diag-card-${card.id}`} data-state={card.state} component="section"
      sx={{ p: 2, bgcolor: md.surfaceContainerLow, minWidth: 0, gridColumn: wide ? '1 / -1' : undefined, scrollMarginTop: 72 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography component="h3" sx={{ fontWeight: 600, fontSize: 16 }}>{t(`admin:diagnostics.cards.${card.id}.title`)}</Typography>
        <CardStateBadge state={card.state} />
        <HelpTip textKey={`admin:diagnostics.cards.${card.id}.purpose`} labelKey="admin:diagnostics.help_label" />
      </Box>
      <Typography variant="body2" sx={{ mt: 0.75 }}>{sentence}</Typography>
      {card.notices && (
        <Typography variant="caption" sx={{ display: 'block', mt: 0.25, color: md.onSurfaceVariant }}>
          {t('admin:diagnostics.cards.common.notices')}
        </Typography>
      )}
      {extra}
      {children}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap', mt: 1.5 }}>
        {target ? (
          <Button size="small" variant="outlined" component={RouterLink} to={target.path}>
            {t('admin:diagnostics.links.open', { page: t(target.nav) })}
          </Button>
        ) : <span />}
        <Button size="small" onClick={onToggle} aria-expanded={expanded}
          endIcon={<ExpandMoreIcon sx={{ transform: expanded ? 'rotate(180deg)' : 'none' }} />}>
          {t('admin:diagnostics.cards.common.details', { count: fmt.count(seriesCount) })}
        </Button>
      </Box>
      <Collapse in={expanded} unmountOnExit>
        <Box sx={{ mt: 1, minWidth: 0 }}>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Button size="small" startIcon={<ContentCopyIcon fontSize="small" />} onClick={copyGroup}>
              {t('admin:diagnostics.cards.common.copy_group')}
            </Button>
          </Box>
          <RawMetricTable families={families} fmt={fmt} windowMs={windowMs} />
        </Box>
      </Collapse>
    </Card>
  )
}
