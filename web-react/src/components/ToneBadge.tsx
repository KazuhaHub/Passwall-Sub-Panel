import type { ComponentType } from 'react'
import { Box, alpha, type SvgIconProps, type Theme } from '@mui/material'
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
import type { CardState, Severity } from '@/utils/diagnostics'

// BADGES ARE DRAWN FROM THE M3 TOKENS, NEVER FROM <Chip color>.
// The theme's MuiChip root and filled overrides (theme/index.ts) replace the
// colour variants' background and text, which is how production ended up
// with five identical grey chips meaning five different things. Every badge
// also carries an icon and its words, so no state is told by colour alone.
//
// Shared by the diagnostics page and the access-control page. The text of a
// tone is its own field: on the amber and the ok tones the ICON keeps the
// hue (a warning triangle, a green check) while the words are drawn in
// onSurface, because amber-on-amber and success-green-on-surface read at about
// 3:1. ToneBadge.contrast.test.ts holds every tone to 4.5:1 in both themes.

export interface Tone {
  /** Fill. */
  bg: string
  /** The icon's colour. */
  fg: string
  /** The words' colour; the icon's when a tone does not say. */
  text?: string
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
    case 'attention': return { ...amber(theme), text: md.onSurface, Icon: WarningAmberIcon }
    case 'ok': return { bg: md.surfaceContainerHigh, fg: theme.palette.success.main, text: md.onSurface, Icon: CheckCircleOutlineIcon }
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
      return { ...amber(theme), text: md.onSurface, Icon: WarningAmberIcon }
    case 'notice':
      return { bg: alpha(theme.palette.info.main, 0.12), fg: theme.palette.info.main, text: md.onSurface, Icon: InfoOutlinedIcon }
  }
}

export interface ToneBadgeProps {
  tone: Tone
  label: string
  testId?: string
  /** `data-state`, for tests and for the styles that key off a state. */
  data?: string
  /** A smaller dot-sized icon (the diagnostics' "recorded"). */
  smallIcon?: boolean
}

/** One badge: icon, words and colour from a tone. */
export default function ToneBadge({ tone, label, testId, data, smallIcon }: ToneBadgeProps) {
  const { Icon } = tone
  return (
    <Box component="span" data-testid={testId} data-state={data}
      sx={{
        display: 'inline-flex', alignItems: 'center', gap: 0.5, flex: '0 0 auto',
        px: 1, py: 0.25, borderRadius: 2, bgcolor: tone.bg, color: tone.text ?? tone.fg,
        fontSize: 12.5, fontWeight: 500, lineHeight: 1.6, whiteSpace: 'nowrap',
      }}>
      <Icon aria-hidden sx={{ fontSize: smallIcon ? 10 : 16, color: tone.fg }} />
      {label}
    </Box>
  )
}
