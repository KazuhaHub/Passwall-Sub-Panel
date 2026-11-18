import { IconButton, Tooltip, useTheme } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'

/**
 * An (i) that explains a concept next to the field it belongs to. The text is
 * the tooltip and the button's accessible name, so it reads the same with a
 * pointer, a keyboard or a screen reader. Pass the translated text.
 */
export default function InfoTip({ text }: { text: string }) {
  const md = useTheme().palette.md
  return (
    <Tooltip title={text} enterTouchDelay={0} slotProps={{ tooltip: { sx: { maxWidth: 380, whiteSpace: 'pre-line' } } }}>
      <IconButton size="small" aria-label={text} sx={{ color: md.onSurfaceVariant }}>
        <InfoOutlinedIcon fontSize="small" />
      </IconButton>
    </Tooltip>
  )
}
