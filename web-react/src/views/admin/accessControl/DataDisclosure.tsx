import { Box, Typography, useTheme } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { useAccessTranslation } from './useAccessTranslation'

/** Neutral collection notice shared by node controls and account records. */
export default function DataDisclosure({ usage = false }: { usage?: boolean }) {
  const { t } = useAccessTranslation('admin')
  const theme = useTheme()
  return <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1, p: 1.5, borderRadius: 2,
    bgcolor: theme.palette.md.surfaceContainer, color: theme.palette.md.onSurfaceVariant }}>
    <InfoOutlinedIcon sx={{ fontSize: 18, mt: 0.25, flexShrink: 0 }} />
    <Typography variant="body2">{t(`admin:access_control.collect.disclosure_${usage ? 'usage' : 'hits'}`)}</Typography>
  </Box>
}
