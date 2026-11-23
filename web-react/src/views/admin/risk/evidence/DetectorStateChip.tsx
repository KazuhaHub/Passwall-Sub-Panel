import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import type { AccessToneKind } from '@/utils/accessControl'
import { useRiskT } from '../useRiskT'
import { stateLabelKey, stateTone, type DetectorState } from './state'

export interface DetectorStateChipProps {
  state: DetectorState
  /** The verdict's reason code: "trusted" on an exempt verdict reads as trust. */
  code?: string
  /** false when the verdict was judged while a server could not be read. */
  complete?: boolean
  tooltip?: string
}

/**
 * The closed colour table's rows (state.ts) on the status tones. Only a flagged state is a danger and
 * only a suspect one a warning; a clean verdict is the quiet dot; every state that is not a verdict
 * (idle, unknown, not computed, switched off) is neutral, never green, and exempt is the notice tone.
 */
const TONE: Partial<Record<AccessToneKind, Tone>> = {
  failing: 'danger', attention: 'warning', ok: 'success', exempt: 'info', paused: 'neutral', none: 'neutral', unknown: 'neutral',
}

/**
 * One detector's state, in the one vocabulary and the one tone scheme every risk surface uses, so a state
 * never reads two ways across tabs. The state is always said in words (a dot or an icon carries only the
 * hue), and a clean verdict judged while some server could not be read is "clean as far as could be
 * seen": it is never drawn green, the colour of a clean bill of health. A suspect or flagged verdict on
 * that floor is, if anything, an understatement, and keeps its tone.
 */
export function DetectorStateChip({ state, code, complete, tooltip }: DetectorStateChipProps) {
  const { t } = useRiskT()
  const partial = state === 'clean' && complete === false
  const badge = (
    <span data-testid="detector-state" data-state={partial ? 'clean_partial' : state} className="inline-flex">
      <StatusBadge tone={TONE[stateTone(state, complete)] ?? 'neutral'} label={t(`admin:${stateLabelKey(state, code)}`)} />
    </span>
  )
  if (!tooltip) return badge
  return (
    <Tooltip>
      <TooltipTrigger render={<span className="inline-flex" />}>{badge}</TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  )
}
