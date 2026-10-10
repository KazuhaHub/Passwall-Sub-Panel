import type { ReactNode } from 'react'
import { Box } from '@mui/material'
export function PipelineRail({ children }: { children: ReactNode }) {
  return <Box sx={{ borderLeft: theme => `2px solid ${theme.palette.md.outlineVariant}`, ml: { xs: 1, sm: 1.5 }, pl: { xs: 2, sm: 3 } }}>{children}</Box>
}
export function PipelineStep({ index, children }: { index: number; children: ReactNode }) {
  return <Box sx={{ position: 'relative', mb: 3 }}><Box aria-hidden sx={{ position: 'absolute', left: { xs: -27, sm: -37 }, top: 5, width: 24, height: 24, borderRadius: '50%', bgcolor: theme => theme.palette.md.surfaceContainerHighest, display: 'grid', placeItems: 'center', fontSize: 13, fontWeight: 600 }}>{index + 1}</Box>{children}</Box>
}
