import type { ReactNode } from 'react'
import { Box, Typography, useTheme, type Theme } from '@mui/material'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import RadioButtonUncheckedIcon from '@mui/icons-material/RadioButtonUnchecked'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { amber, type Tone } from './ToneBadge'

// THE ONE SUMMARY ON A PAGE. Drawn from the same tokens as the badges, not an
// <Alert severity>, for the same reason the badges are not chips: the theme's
// overrides would decide the colour, and the colour has to come from the worst
// finding. The shell only: the tone, the title, an optional detail and meta
// line and a slot for actions. What the words say is the page's.

export type StatusLineTone = 'failing' | 'attention' | 'measuring' | 'ok' | 'quiet'

export function statusLineTone(theme: Theme, tone: StatusLineTone): Tone & { text: string } {
  const md = theme.palette.md
  switch (tone) {
    case 'failing':
      return { bg: md.errorContainer, fg: md.onErrorContainer, text: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'attention': {
      const a = amber(theme)
      return { ...a, text: md.onSurface, Icon: WarningAmberIcon }
    }
    case 'measuring':
      return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, text: md.onSecondaryContainer, Icon: HourglassEmptyIcon }
    case 'ok':
      return { bg: md.surfaceContainerHigh, fg: theme.palette.success.main, text: md.onSurface, Icon: CheckCircleOutlineIcon }
    case 'quiet':
      // Nothing to report and nothing wrong: never drawn green.
      return { bg: md.surfaceContainerHighest, fg: md.onSurfaceVariant, text: md.onSurfaceVariant, Icon: RadioButtonUncheckedIcon }
  }
}

export default function StatusLine({
  tone, title, detail, meta, actions, testId, dataTone, liveRegion = 'box', detailHidden = false, metaHidden,
}: {
  tone: StatusLineTone
  title: ReactNode
  /** The second line: what the verdict is made of. */
  detail?: ReactNode
  /** The third, smaller line (when it was read). */
  meta?: ReactNode
  /** Buttons, right-aligned beside the text (below it on a phone). */
  actions?: ReactNode
  testId?: string
  /** `data-tone`, for a page whose own tone set is finer than this one's. */
  dataTone?: string
  /**
   * What a screen reader announces when the line changes: the whole box, or
   * only the title. A line with a ticking countdown or a read-at time in it
   * wants 'title', with those two in detailHidden.
   */
  liveRegion?: 'box' | 'title'
  /** Marks the detail line aria-hidden. */
  detailHidden?: boolean
  /** Marks the meta line aria-hidden; defaults to detailHidden. */
  metaHidden?: boolean
}) {
  const theme = useTheme()
  const t = statusLineTone(theme, tone)
  const { Icon } = t
  const titleEl = (
    <Typography role={liveRegion === 'title' ? 'status' : undefined}
      sx={{ fontWeight: 600, color: tone === 'ok' ? t.fg : t.text }}>{title}</Typography>
  )
  return (
    <Box data-testid={testId} data-tone={dataTone ?? tone} role={liveRegion === 'box' ? 'status' : undefined}
      sx={{
        display: 'flex', gap: 1.5, alignItems: { xs: 'stretch', sm: 'flex-start' }, flexDirection: { xs: 'column', sm: 'row' },
        p: 2, mb: 2, borderRadius: 3, bgcolor: t.bg,
      }}>
      <Box sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start', flex: 1, minWidth: 0 }}>
        <Icon aria-hidden sx={{ color: t.fg, mt: 0.25 }} />
        <Box sx={{ minWidth: 0 }}>
          {titleEl}
          {detail ? (
            <Typography variant="body2" aria-hidden={detailHidden || undefined} component="div" sx={{ mt: 0.5, color: t.text }}>
              {detail}
            </Typography>
          ) : null}
          {meta ? (
            <Typography variant="caption" aria-hidden={(metaHidden ?? detailHidden) || undefined} component="div"
              sx={{ display: 'block', mt: 0.25, color: t.text, opacity: 0.8 }}>
              {meta}
            </Typography>
          ) : null}
        </Box>
      </Box>
      {actions ? <Box sx={{ flex: '0 0 auto', display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>{actions}</Box> : null}
    </Box>
  )
}
