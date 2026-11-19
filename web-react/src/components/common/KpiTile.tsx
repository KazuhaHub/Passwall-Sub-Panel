import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { TONE_STYLE, type Tone } from './tones'

/**
 * One figure: what it is, the number, and what it is out of. Static by default;
 * with `onToggle` it is a filter in tile form: a button with aria-pressed, pressed
 * shown by the brand ring and a tinted fill. A second click is the page's cue to
 * clear the filter.
 *
 * A `tone` (from common/tones.ts, the one source of tone colours) draws a thin bar on
 * the left edge; only success and danger also colour the figure itself.
 */
export function KpiTile({ label, value, caption, danger, tone, icon, title, pressed, onToggle, className }: {
  label: string
  /** undefined shows a dash: "not known yet" must not read as zero. */
  value: ReactNode
  caption?: ReactNode
  /** Only for a figure that is itself a failure; shorthand for tone="danger". */
  danger?: boolean
  tone?: Tone
  icon?: ReactNode
  /** Raw values behind a folded figure, on hover. */
  title?: string
  pressed?: boolean
  onToggle?: () => void
  className?: string
}) {
  const t: Tone = tone ?? (danger ? 'danger' : 'neutral')
  const body = (
    <>
      {t !== 'neutral' && <span data-slot="kpi-tone-bar" aria-hidden="true" className={cn('absolute inset-y-2 left-0 w-0.5 rounded-full', TONE_STYLE[t].dot)} />}
      <span className="flex w-full items-center gap-1.5 text-xs text-muted-foreground [&_svg]:size-3.5">{icon}{label}</span>
      {/* Amber text on white is too faint at this weight, so warning and info keep the plain foreground. */}
      <span className={cn('block text-[1.0625rem] leading-snug font-semibold tabular-nums [overflow-wrap:anywhere]', t === 'danger' ? 'text-destructive' : t === 'success' ? 'text-success' : 'text-foreground')}>{value === undefined ? '—' : value}</span>
      {caption ? <span className="block text-xs text-muted-foreground tabular-nums">{caption}</span> : null}
    </>
  )
  // One layout for both variants. A <button> centres its content vertically and a <div>
  // does not, so in a row the labels of a clickable and a plain tile sat ~9px apart;
  // both are a top-aligned column of the same minimum height now.
  const base = 'relative flex min-h-11 min-w-0 flex-col items-start justify-start rounded-lg bg-card px-3 py-2.5 text-left ring-1 ring-foreground/10'
  if (!onToggle) {
    return <div data-slot="kpi-tile" title={title} className={cn(base, className)}>{body}</div>
  }
  return (
    <button
      type="button"
      data-slot="kpi-tile"
      title={title}
      aria-pressed={pressed === true}
      onClick={onToggle}
      className={cn(
        base,
        'w-full outline-none transition-colors hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50',
        pressed && 'bg-brand/10 ring-2 ring-brand hover:bg-brand/15',
        className,
      )}
    >
      {body}
    </button>
  )
}

/** Two tiles a row on a phone, as many as fit on a wider screen. */
export function KpiGrid({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('my-3 grid grid-cols-2 gap-2 md:grid-cols-[repeat(auto-fit,minmax(10.5rem,1fr))]', className)}>{children}</div>
}
