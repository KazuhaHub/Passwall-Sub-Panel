import { IconButton, Tooltip } from '@mui/material'
import DensitySmallIcon from '@mui/icons-material/DensitySmall'
import DensityMediumIcon from '@mui/icons-material/DensityMedium'
import { useTranslation } from 'react-i18next'

import { useAppearanceStore } from '@/stores/appearance'

// DensityToggle is a one-click switch in the admin topbar between MUI's
// default spacing (comfortable) and Cloudreve-style compact (denser
// tables, tighter inputs, smaller h4 page titles). State lives in
// stores/appearance and persists in localStorage; the theme rebuilds
// on every toggle via App.tsx's useMemo dependency.
export default function DensityToggle() {
  const { t } = useTranslation('appearance')
  const density = useAppearanceStore(s => s.density)
  const setDensity = useAppearanceStore(s => s.setDensity)
  const isCompact = density === 'compact'
  const label = t(isCompact ? 'density.switch_to_comfortable' : 'density.switch_to_compact')
  return (
    <Tooltip title={label}>
      <IconButton aria-label={label} onClick={() => setDensity(isCompact ? 'comfortable' : 'compact')} sx={{ ml: 0.5 }}>
        {isCompact ? <DensitySmallIcon /> : <DensityMediumIcon />}
      </IconButton>
    </Tooltip>
  )
}
