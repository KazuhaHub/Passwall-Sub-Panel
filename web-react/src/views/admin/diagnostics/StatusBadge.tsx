import type { ComponentType } from 'react'
import { Box, alpha, useTheme, type SvgIconProps, type Theme } from '@mui/material'
import BlockIcon from '@mui/icons-material/Block'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import CircleIcon from '@mui/icons-material/Circle'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import HelpOutlineIcon from '@mui/icons-material/HelpOutlineOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import RadioButtonUncheckedIcon from '@mui/icons-material/RadioButtonUnchecked'
import RemoveCircleOutlineIcon from '@mui/icons-material/RemoveCircleOutlineOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'
import type { CardState, Severity } from '@/utils/diagnostics'

// THE PAGE'S BADGES ARE DRAWN FROM THE M3 TOKENS, NEVER FROM <Chip color>.
// The theme's MuiChip root and filled overrides (theme/index.ts) replace the
// colour variants' background and text, which is how production ended up
// with five identical grey chips meaning five different things. Every badge
// also carries an icon and its words, so no state is told by colour alone.

export interface Tone {
  bg: string
  fg: string
  Icon: ComponentType<SvgIconProps>
}

/** The amber pair: on the light theme the dark warning reads, on the dark
 *  theme the light one does. */
export function amber(theme: Theme): { bg: string; fg: string } {
  return {
    bg: alpha(theme.palette.warning.main, 0.16),
    fg: theme.palette.mode === 'dark' ? theme.palette.warning.light : theme.palette.warning.dark,
  }
}

export function stateTone(theme: Theme, state: CardState): Tone {
  const md = theme.palette.md
  const quiet = { bg: md.surfaceContainerHighest, fg: md.onSurfaceVariant }
  switch (state) {
    case 'failing': return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'attention': return { ...amber(theme), Icon: WarningAmberIcon }
    case 'ok': return { bg: md.surfaceContainerHigh, fg: theme.palette.success.main, Icon: CheckCircleOutlineIcon }
    case 'idle': return { ...quiet, Icon: RemoveCircleOutlineIcon }
    case 'measuring': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: HourglassEmptyIcon }
    case 'inhibited': return { ...quiet, Icon: HelpOutlineIcon }
    case 'not_applicable': return { ...quiet, Icon: BlockIcon }
    case 'none': return { ...quiet, Icon: RadioButtonUncheckedIcon }
    case 'recorded': return { ...quiet, Icon: CircleIcon }
  }
}

export function severityTone(theme: Theme, severity: Severity): Tone {
  const md = theme.palette.md
  switch (severity) {
    case 'critical':
    case 'error':
      return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'warn':
      return { ...amber(theme), Icon: WarningAmberIcon }
    case 'notice':
      return { bg: alpha(theme.palette.info.main, 0.12), fg: theme.palette.info.main, Icon: InfoOutlinedIcon }
  }
}

function Badge({ tone, label, testId, data }: { tone: Tone; label: string; testId: string; data: string }) {
  const { Icon } = tone
  return (
    <Box component="span" data-testid={testId} data-state={data}
      sx={{
        display: 'inline-flex', alignItems: 'center', gap: 0.5, flex: '0 0 auto',
        px: 1, py: 0.25, borderRadius: 2, bgcolor: tone.bg, color: tone.fg,
        fontSize: 12.5, fontWeight: 500, lineHeight: 1.6, whiteSpace: 'nowrap',
      }}>
      <Icon aria-hidden sx={{ fontSize: data === 'recorded' ? 10 : 16 }} />
      {label}
    </Box>
  )
}

/** One area card's state: icon, words and colour from one closed table. */
export function CardStateBadge({ state }: { state: CardState }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  return <Badge tone={stateTone(theme, state)} label={t(`admin:diagnostics.state.${state}`)} testId="state-badge" data={state} />
}

/** A finding's severity, in the same colours as the card states. */
export function SeverityBadge({ severity }: { severity: Severity }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  return <Badge tone={severityTone(theme, severity)} label={t(`admin:diagnostics.severity.${severity}`)} testId="severity-badge" data={severity} />
}
