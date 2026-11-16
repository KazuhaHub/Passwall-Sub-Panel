import { Typography, useTheme } from '@mui/material'
import type { PreviewHint } from '@/hooks/useDraftPreview'

/**
 * The one line an edit-time preview shows (ADR 0042 §6): the matched items,
 * "none" in the error colour, or "cannot be determined". Nothing while the
 * first answer is pending, so an empty state never flashes.
 */
export default function PreviewCaption({ hint, readyText, noneText, unknownText, testId = 'preview-caption', noneTone = 'error' }: {
  hint?: PreviewHint
  readyText: (items: string[]) => string
  noneText: string
  unknownText: string
  testId?: string
  /** "none" is a warning by default (nothing would match); `neutral` when an
   *  empty answer is good news, e.g. a group change that changes nothing. */
  noneTone?: 'error' | 'neutral'
}) {
  const md = useTheme().palette.md
  if (!hint || hint.state === 'loading') return null
  const none = hint.state === 'ready' && hint.items.length === 0
  return <Typography data-testid={testId} sx={{ fontSize: 12, color: none && noneTone === 'error' ? md.error : md.onSurfaceVariant }}>
    {hint.state === 'error' ? unknownText : none ? noneText : readyText(hint.items)}
  </Typography>
}
