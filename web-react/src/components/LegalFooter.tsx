import { Box, Typography, useTheme } from '@mui/material'
import LegalLinks from './LegalLinks'

export default function LegalFooter({ text, enabled }: { text: string; enabled: boolean }) {
  const md = useTheme().palette.md
  if (!text && !enabled) return null
  return <Box component="footer" sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'center', columnGap: 2, px: 2, py: 1, fontSize: 12, color: md.onSurfaceVariant }}>
    {text && <Typography component="span" sx={{ fontSize: 'inherit', overflowWrap: 'anywhere' }}>{text}</Typography>}
    {enabled && <LegalLinks />}
  </Box>
}
