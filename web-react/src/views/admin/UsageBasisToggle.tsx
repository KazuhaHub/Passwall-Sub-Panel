import { ToggleButton, ToggleButtonGroup } from '@mui/material'
import { useTranslation } from 'react-i18next'

export type UsageBasis = 'charged' | 'raw'

/** Raw / charged switch shared by the per-server and per-node usage tables. */
export function UsageBasisToggle({ value, onChange }: { value: UsageBasis; onChange: (v: UsageBasis) => void }) {
  const { t } = useTranslation('admin')
  return (
    <ToggleButtonGroup
      size="small" exclusive value={value}
      aria-label={t('users.nodeusage.mode_label', { defaultValue: '流量口径' })}
      onChange={(_, v) => { if (v) onChange(v as UsageBasis) }}
      sx={{ '& .MuiToggleButton-root': { px: 1.5, py: 0.25, fontSize: 12 } }}
    >
      <ToggleButton value="charged">{t('users.nodeusage.mode_charged', { defaultValue: '计费' })}</ToggleButton>
      <ToggleButton value="raw">{t('users.nodeusage.mode_raw', { defaultValue: '原始' })}</ToggleButton>
    </ToggleButtonGroup>
  )
}
