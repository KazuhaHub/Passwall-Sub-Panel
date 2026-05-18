import { IconButton, Tooltip } from '@mui/material'
import DensitySmallIcon from '@mui/icons-material/DensitySmall'
import DensityMediumIcon from '@mui/icons-material/DensityMedium'

import { useAppearanceStore } from '@/stores/appearance'

// DensityToggle is a one-click switch in the admin topbar between MUI's
// default spacing (comfortable) and Cloudreve-style compact (denser
// tables, tighter inputs, smaller h4 page titles). State lives in
// stores/appearance and persists in localStorage; the theme rebuilds
// on every toggle via App.tsx's useMemo dependency.
export default function DensityToggle() {
  const density = useAppearanceStore(s => s.density)
  const setDensity = useAppearanceStore(s => s.setDensity)
  const isCompact = density === 'compact'
  return (
    <Tooltip title={isCompact ? 'Switch to comfortable density' : 'Switch to compact density'}>
      <IconButton onClick={() => setDensity(isCompact ? 'comfortable' : 'compact')} sx={{ ml: 0.5 }}>
        {isCompact ? <DensitySmallIcon /> : <DensityMediumIcon />}
      </IconButton>
    </Tooltip>
  )
}
