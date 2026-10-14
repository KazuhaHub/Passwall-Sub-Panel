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

export interface Tone {
  bg: string
  /** Text colour; icons may retain their semantic colour independently. */
  fg: string
  iconColor?: string
  Icon: ComponentType<SvgIconProps>
}

export function amber(theme: Theme): { bg: string; fg: string } {
  return {
    bg: alpha(theme.palette.warning.main, 0.16),
    fg: theme.palette.mode === 'dark' ? theme.palette.warning.light : theme.palette.warning.dark,
  }
}

export function stateTone(theme: Theme, state: CardState | 'quiet'): Tone {
  const md = theme.palette.md
  const quiet = { bg: md.surfaceContainerHighest, fg: md.onSurfaceVariant }
  switch (state) {
    case 'failing': return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'attention': return { bg: amber(theme).bg, fg: md.onSurface, iconColor: amber(theme).fg, Icon: WarningAmberIcon }
    case 'ok': return { bg: md.surfaceContainerHigh, fg: md.onSurface, iconColor: theme.palette.success.main, Icon: CheckCircleOutlineIcon }
    case 'idle': return { ...quiet, Icon: RemoveCircleOutlineIcon }
    case 'measuring': return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, Icon: HourglassEmptyIcon }
    case 'inhibited': return { ...quiet, Icon: HelpOutlineIcon }
    case 'not_applicable': return { ...quiet, Icon: BlockIcon }
    case 'quiet':
    case 'none': return { ...quiet, Icon: RadioButtonUncheckedIcon }
    case 'recorded': return { ...quiet, Icon: CircleIcon }
  }
}

export function severityTone(theme: Theme, severity: Severity): Tone {
  const md = theme.palette.md
  switch (severity) {
    case 'critical':
    case 'error': return { bg: md.errorContainer, fg: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'warn': return { bg: amber(theme).bg, fg: md.onSurface, iconColor: amber(theme).fg, Icon: WarningAmberIcon }
    case 'notice': return { bg: alpha(theme.palette.info.main, 0.12), fg: md.onSurface, iconColor: theme.palette.info.main, Icon: InfoOutlinedIcon }
  }
}

// M3 tokens define both surfaces and text. The icon and label convey the
// state together, so its meaning never depends on colour alone.
export function ToneBadge({ tone, label, testId, data }: {
  tone: Tone
  label: string
  testId?: string
  data?: string
}) {
  const { Icon } = tone
  return (
    <Box component="span" data-testid={testId} data-state={data}
      sx={{
        display: 'inline-flex', alignItems: 'center', gap: 0.5, flex: '0 0 auto',
        px: 1, py: 0.25, borderRadius: 2, bgcolor: tone.bg, color: tone.fg,
        fontSize: 12.5, fontWeight: 500, lineHeight: 1.6, whiteSpace: 'nowrap',
      }}>
      <Icon aria-hidden sx={{ fontSize: data === 'recorded' ? 10 : 16, color: tone.iconColor ?? tone.fg }} />
      {label}
    </Box>
  )
}
