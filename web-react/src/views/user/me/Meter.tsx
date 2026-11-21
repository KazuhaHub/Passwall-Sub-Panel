import { cn } from '@/lib/utils'
import { TONE_STYLE, type Tone } from '@/components/common/tones'

/**
 * A thin bar for "how much of a limit is used". The figure it stands for is always printed next to it, so the bar
 * is the glance and never the only carrier of the number or of the state (its tone is also said in words by the caller).
 */
export function Meter({ percent, tone = 'success', label, className }: {
  percent: number
  tone?: Tone
  /** What the bar measures, for a screen reader. */
  label: string
  className?: string
}) {
  const value = Math.max(0, Math.min(100, percent))
  return (
    <div
      role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value)}
      className={cn('h-2 overflow-hidden rounded-full bg-muted', className)}
    >
      <div className={cn('h-full rounded-full transition-[width] duration-300', TONE_STYLE[tone].dot)} style={{ width: `${value}%` }} />
    </div>
  )
}
