import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { TONE_STYLE, type Tone } from './tones'

/**
 * The quiet form of a status: a dot, with its words. The dot alone is never the
 * whole message: `label` is always present (visible, or for a screen reader when
 * `hideLabel` keeps the row compact), so no state is told by colour alone.
 */
export function StatusDot({ tone = 'neutral', label, hideLabel = false, className, children }: {
  tone?: Tone
  label: string
  hideLabel?: boolean
  className?: string
  /** Replaces the label text where the row has richer content to show. */
  children?: ReactNode
}) {
  return (
    <span data-slot="status-dot" data-tone={tone} className={cn('inline-flex items-center gap-1.5 text-sm', className)}>
      <span aria-hidden className={cn('size-2 shrink-0 rounded-full', TONE_STYLE[tone].dot)} />
      {hideLabel ? <span className="sr-only">{label}</span> : (children ?? label)}
    </span>
  )
}
