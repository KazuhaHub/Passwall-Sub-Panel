import { Box, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { PRESET_NAMES, type PresetName } from './presets'

const P = 'admin:risk_center.policy.'

/**
 * A card's preset: 宽松 / 标准 / 严格, plus 自定义. The lit one is DETECTED
 * from the draft (detectPreset), never stored, so a hand-typed value lights
 * 自定义 at once. 自定义 cannot be clicked — there is nothing to apply — and
 * is lit only when no preset matches.
 */
export default function PresetSelect({ value, onApply }: {
  value: PresetName
  onApply: (preset: Exclude<PresetName, 'custom'>) => void
}) {
  const { t } = useTranslation(['admin'])
  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: 13, color: 'text.secondary' }}>{t(`${P}preset`)}</Typography>
        <ToggleButtonGroup exclusive size="small" value={value} aria-label={t(`${P}preset`)}
          onChange={(_, v: PresetName | null) => { if (v && v !== 'custom') onApply(v) }}>
          {PRESET_NAMES.map(name => (
            <ToggleButton key={name} value={name} sx={{ px: 1.5 }}>{t(`${P}preset_${name}`)}</ToggleButton>
          ))}
          <ToggleButton value="custom" disabled sx={{ px: 1.5 }}>{t(`${P}preset_custom`)}</ToggleButton>
        </ToggleButtonGroup>
      </Box>
      <Typography sx={{ fontSize: 12, color: 'text.secondary', mt: 0.5 }}>{t(`${P}preset_hint`)}</Typography>
    </Box>
  )
}
