import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { TONE_SHAPE, TONE_STYLE, type Tone } from './tones'

/**
 * The bare marker, for a place that already says the state in words (a path node whose
 * status is also in the summary sentence). Anything that stands alone uses StatusDot,
 * which carries the words. This is the one place a status marker is drawn.
 *
 * Each tone has its own SHAPE as well as its colour (TONE_SHAPE): a filled circle, a
 * triangle, a square (danger, the heaviest), a diamond and a hollow ring. Colour alone cannot carry a state
 * (WCAG 1.4.1), and the warning, danger and neutral hues are close in lightness in the
 * light theme. The shapes are inline SVG in an 8-unit box, drawn with whole or half
 * units so they stay crisp at the 8px size; `fill` / `stroke` are the text colour, which
 * is the tone's ink. A caller's `size-*` resizes the wrapper and the drawing follows.
 */
export function ToneDot({ tone = 'neutral', className }: { tone?: Tone; className?: string }) {
  const shape = TONE_SHAPE[tone]
  return (
    <span
      aria-hidden="true"
      data-slot="tone-dot"
      data-shape={shape}
      className={cn('inline-flex size-2 shrink-0 items-center justify-center rounded-full', TONE_STYLE[tone].ink, className)}
    >
      <svg viewBox="0 0 8 8" className="size-full" focusable="false">
        {shape === 'circle' && <circle cx="4" cy="4" r="3.75" fill="currentColor" />}
        {shape === 'triangle' && <polygon points="4,0 8,8 0,8" fill="currentColor" />}
        {shape === 'diamond' && <polygon points="4,0 8,4 4,8 0,4" fill="currentColor" />}
        {shape === 'square' && <rect x="0.25" y="0.25" width="7.5" height="7.5" rx="0.75" fill="currentColor" />}
        {shape === 'ring' && <circle cx="4" cy="4" r="3.25" fill="none" stroke="currentColor" strokeWidth="1.5" />}
      </svg>
    </span>
  )
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
