import { Chip, Tooltip } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { stateColor, stateLabelKey, type DetectorState } from './state'

export interface DetectorStateChipProps {
  state: DetectorState
  /** The verdict's reason code: "trusted" on an exempt verdict reads as trust. */
  code?: string
  /** false when the verdict was judged while a panel could not be read. */
  complete?: boolean
  tooltip?: string
}

/**
 * One detector's state, in the one vocabulary and the one colour scheme every
 * risk surface uses, so a state never reads two ways across tabs.
 *
 * A clean verdict judged while some panel could not be read stands on a floor
 * of the account's sources — "clean as far as could be seen" — so it is never
 * drawn green, the colour of a clean bill of health. A suspect or flagged
 * verdict on a floor is, if anything, an understatement, and keeps its colour.
 */
export function DetectorStateChip({ state, code, complete, tooltip }: DetectorStateChipProps) {
  const { t } = useTranslation(['admin'])
  const c = stateColor(state)
  const chip = (
    <Chip size="small" color={complete === false && c === 'success' ? 'default' : c}
      variant={state === 'not_computed' ? 'outlined' : 'filled'}
      label={t(`admin:${stateLabelKey(state, code)}`)} />
  )
  return tooltip ? <Tooltip title={tooltip}>{chip}</Tooltip> : chip
}
