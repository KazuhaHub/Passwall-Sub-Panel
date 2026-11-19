import { IconCircle, IconError, IconInfo, IconSuccess, IconWarning, type IconType } from '@/lib/icons'

import { cn } from '@/lib/utils'
import { StatusDot } from './StatusDot'
import { TONE_STYLE, type Tone } from './tones'

const ICON: Record<Tone, IconType> = {
  success: IconSuccess,
  warning: IconWarning,
  danger: IconError,
  info: IconInfo,
  neutral: IconCircle,
}

/**
 * A status, quiet unless it is abnormal. Success and neutral render as a dot and
 * plain words; warning, danger and info render as a tinted badge with an icon.
 * `quiet` overrides that either way (a danger shown as a dot in a dense list, a
 * success shown as a badge on a detail page). The words are always text, in the
 * foreground colour; only the dot and icon carry the hue.
 */
export function StatusBadge({ tone = 'neutral', label, quiet, className, testId }: {
  tone?: Tone
  label: string
  quiet?: boolean
  className?: string
  testId?: string
}) {
  const style = TONE_STYLE[tone]
  const asDot = quiet ?? !style.loud
  if (asDot) {
    return <StatusDot tone={tone} label={label} className={cn('text-foreground', className)} />
  }
  const Icon = ICON[tone]
  return (
    <span
      data-slot="status-badge"
      data-tone={tone}
      data-testid={testId}
      className={cn(
        'inline-flex w-fit shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-xs/5 font-medium whitespace-nowrap text-foreground ring-1 ring-inset',
        style.tint, style.ring, className,
      )}
    >
      <Icon aria-hidden className={cn('size-3.5 shrink-0', style.ink)} />
      {label}
    </span>
  )
}
