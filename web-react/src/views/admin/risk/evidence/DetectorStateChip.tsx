import { Tooltip, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { severityTone, stateTone, ToneBadge } from '@/components/ToneBadge'

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
  const theme = useTheme()
  const c = complete === false && stateColor(state) === 'success' ? 'default' : stateColor(state)
  const paint = c === 'error' ? 'failing' : c === 'warning' ? 'attention' : c === 'success' ? 'ok' : c === 'info' ? 'notice' : 'quiet'
  const tone = paint === 'notice' ? severityTone(theme, 'notice') : stateTone(theme, paint)
  const badge = <ToneBadge tone={tone} data={paint} label={t(`admin:${stateLabelKey(state, code)}`)} />
  return tooltip ? <Tooltip title={tooltip}><span>{badge}</span></Tooltip> : badge
}
