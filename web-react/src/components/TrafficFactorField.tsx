import { InputAdornment, TextField, Tooltip } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { useTranslation } from 'react-i18next'

import { factorTextToMilli } from '@/utils/trafficFactor'

/**
 * Decimal multiplier input (1.5 <-> 1500). The typed text lives in the parent
 * form, so the form can see an invalid/empty value and block submit; the parent
 * stores milli only for valid text.
 *
 * `compact` replaces the long helper text with an info tooltip, for repeated rows.
 */
export function TrafficFactorField({ text, onText, label, compact = false, size = 'small' }: {
  text: string
  onText: (text: string, milli: number | null) => void
  label?: string
  compact?: boolean
  size?: 'small' | 'medium'
}) {
  const { t } = useTranslation('admin')
  const parsed = factorTextToMilli(text)
  const hint = t('nodes.hint.traffic_factor')
  return (
    <TextField
      size={size} type="number" fullWidth
      label={label ?? t('nodes.field.traffic_factor')}
      value={text}
      onChange={e => onText(e.target.value, factorTextToMilli(e.target.value))}
      error={parsed === null}
      helperText={parsed === null ? t('nodes.validate.traffic_factor') : compact ? undefined : hint}
      slotProps={{
        htmlInput: { min: 0, max: 100, step: 0.001, inputMode: 'decimal' },
        input: compact ? {
          endAdornment: <InputAdornment position="end">
            <Tooltip title={hint}><InfoOutlinedIcon fontSize="small" aria-label={hint} /></Tooltip>
          </InputAdornment>,
        } : undefined,
      }}
    />
  )
}
