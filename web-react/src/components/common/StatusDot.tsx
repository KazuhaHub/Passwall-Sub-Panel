import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { TONE_STYLE, type Tone } from './tones'

/**
 * The bare dot, for a place that already says the state in words (a path node whose
 * status is also in the summary sentence). Anything that stands alone uses StatusDot,
 * which carries the words. This is the one place a status dot is drawn.
 */
export function ToneDot({ tone = 'neutral', className }: { tone?: Tone; className?: string }) {
  return <span aria-hidden="true" data-slot="tone-dot" className={cn('inline-block size-2 shrink-0 rounded-full', TONE_STYLE[tone].dot, className)} />
}

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
      <ToneDot tone={tone} />
      {hideLabel ? <span className="sr-only">{label}</span> : (children ?? label)}
    </span>
  )
}
