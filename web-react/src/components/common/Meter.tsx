import { cn } from '@/lib/utils'
import { TONE_STYLE, type Tone } from './tones'

/**
 * A thin bar for "how much of a limit is used". The figure it stands for is always printed next to it, so the bar
 * is the glance and never the only carrier of the number or of the state (a caller says the tone in words too).
 *
 * With a `label` it is a progressbar for assistive tech (value, 0 to `max`); without one it is decoration and is
 * hidden from them, for a row whose numbers already say the same. The fill is the colour of `tone`, or any
 * background class in `barClassName` for a caller with its own thresholds. Height and width come from `className`
 * (default: 8px tall, full width).
 */
export function Meter({ value, max = 100, label, tone = 'success', barClassName, className }: {
  value: number
  max?: number
  label?: string
  tone?: Tone
  barClassName?: string
  className?: string
}) {
  const clamped = max > 0 ? Math.max(0, Math.min(max, value)) : 0
  const percent = max > 0 ? (clamped / max) * 100 : 0
  return (
    <div
      {...(label
        ? { role: 'progressbar', 'aria-label': label, 'aria-valuemin': 0, 'aria-valuemax': max, 'aria-valuenow': Math.round(clamped) }
        : { 'aria-hidden': true })}
      className={cn('h-2 w-full overflow-hidden rounded-full bg-muted', className)}
    >
      <div className={cn('h-full rounded-full transition-[width] duration-300', barClassName ?? TONE_STYLE[tone].dot)} style={{ width: `${percent}%` }} />
    </div>
  )
}
