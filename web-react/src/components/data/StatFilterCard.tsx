import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type StatTone = 'neutral' | 'success' | 'warning' | 'danger'

export interface StatFilterCardProps {
  label: string
  /** undefined shows a dash: "not known yet" must not read as zero. */
  value: string | number | undefined
  caption?: ReactNode
  tone?: StatTone
  icon?: ReactNode
  /** Present: the card is a toggle filter. Absent: a plain tile. */
  onToggle?: () => void
  /** The filter this card stands for is on. Pressing again takes it off. */
  pressed?: boolean
  className?: string
}

const VALUE_TONE: Record<StatTone, string> = {
  neutral: 'text-foreground',
  success: 'text-success',
  // Amber text on white is too faint to read at 2xl weight; the tone is carried by the bar and dot instead.
  warning: 'text-foreground',
  danger: 'text-destructive',
}
const BAR_TONE: Record<StatTone, string> = {
  neutral: 'bg-border',
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-destructive',
}

/**
 * A number that is also a filter: the metric tile of the risk queue, made
 * shared. As a toggle it is a real button with aria-pressed; a second press
 * clears the filter, so there is no separate "clear" to look for.
 */
export function StatFilterCard({ label, value, caption, tone = 'neutral', icon, onToggle, pressed, className }: StatFilterCardProps) {
  const body = (
    <>
      <span aria-hidden="true" className={cn('absolute inset-y-2 left-0 w-0.5 rounded-full', BAR_TONE[tone])} />
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground [&_svg]:size-3.5">
        {icon}
        {label}
      </span>
      <span className={cn('text-2xl leading-tight font-semibold tabular-nums', VALUE_TONE[tone])}>{value === undefined ? '—' : value}</span>
      {caption && <span className="text-xs text-muted-foreground">{caption}</span>}
    </>
  )
  const base = 'relative flex min-w-0 flex-col items-start gap-0.5 rounded-xl border bg-card px-3.5 py-2.5 text-left'
  if (!onToggle) {
    return <div data-slot="stat-card" className={cn(base, className)}>{body}</div>
  }
  return (
    <button
      type="button"
      data-slot="stat-card"
      aria-pressed={pressed === true}
      onClick={onToggle}
      className={cn(
        base,
        'cursor-pointer outline-none transition-colors hover:border-foreground/30 focus-visible:ring-3 focus-visible:ring-ring/50',
        pressed && 'border-brand bg-brand/[0.07] ring-1 ring-brand/50',
        className,
      )}
    >
      {body}
    </button>
  )
}

/** Two columns on a phone, up to `columns` from sm up. */
export function StatFilterGrid({ children, columns = 4, className }: { children: ReactNode; columns?: 2 | 3 | 4 | 5 | 6; className?: string }) {
  const cols = { 2: 'sm:grid-cols-2', 3: 'sm:grid-cols-3', 4: 'sm:grid-cols-4', 5: 'sm:grid-cols-5', 6: 'sm:grid-cols-6' }[columns]
  return <div className={cn('grid grid-cols-2 gap-3', cols, className)}>{children}</div>
}
