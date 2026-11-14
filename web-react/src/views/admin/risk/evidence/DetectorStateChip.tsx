import { Tooltip, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import ToneBadge from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
import { stateLabelKey, stateTone, type DetectorState } from './state'

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
 * risk surface uses, so a state never reads two ways across tabs. It is a
 * ToneBadge from the access-control page's closed colour table, with an icon
 * and its words: no state is told by colour alone, and no <Chip color> says it.
 *
 * A clean verdict judged while some panel could not be read stands on a floor
 * of the account's sources — "clean as far as could be seen" — so it is never
 * drawn green, the colour of a clean bill of health. A suspect or flagged
 * verdict on a floor is, if anything, an understatement, and keeps its colour.
 */
export function DetectorStateChip({ state, code, complete, tooltip }: DetectorStateChipProps) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const partial = state === 'clean' && complete === false
  const badge = (
    <ToneBadge tone={accessTone(theme, stateTone(state, complete))} label={t(`admin:${stateLabelKey(state, code)}`)}
      testId="detector-state" data={partial ? 'clean_partial' : state} />
  )
  return tooltip ? <Tooltip title={tooltip}><span>{badge}</span></Tooltip> : badge
}
