import { ToneDot } from '@/components/common/StatusDot'
import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { CheckIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface OptionCardOption<V extends string> {
  value: V
  title: ReactNode
  /** One line: what choosing this does. */
  description: ReactNode
  /** A small picture of the choice (a PathDiagram at size sm). Decorative: title and description carry the meaning. */
  diagram?: ReactNode
  disabled?: boolean
  /** Why it cannot be chosen, shown on the card itself. Required in spirit: a greyed card with no reason is a dead end. */
  disabledReason?: ReactNode
}

export interface OptionCardsProps<V extends string> {
  value: V | null
  onChange: (next: V) => void
  options: readonly OptionCardOption<V>[]
  'aria-label'?: string
  'aria-labelledby'?: string
  /** Columns from the md breakpoint up; one column below it. */
  columns?: 1 | 2 | 3 | 4
  className?: string
}

const COLS = { 1: 'md:grid-cols-1', 2: 'md:grid-cols-2', 3: 'md:grid-cols-3', 4: 'md:grid-cols-4' } as const

/**
 * A single choice shown as cards with a picture, in place of a type dropdown
 * (plan 3.6). It is a radio group: one tab stop, arrow keys move and select,
 * Space or Enter selects. A card that cannot be chosen stays visible with its
 * reason beside it, and is skipped by the arrow keys.
 */
export function OptionCards<V extends string>({
  value, onChange, options, columns = 2, className, ...aria
}: OptionCardsProps<V>) {
  const uid = useId()
  const refs = useRef<(HTMLDivElement | null)[]>([])
  const enabled = options.map((o, i) => (o.disabled ? -1 : i)).filter(i => i >= 0)
  const current = options.findIndex(o => o.value === value)
  const tabStop = enabled.includes(current) ? current : enabled[0]

  const onKey = (e: KeyboardEvent, i: number) => {
    if (e.key === ' ' || e.key === 'Enter') {
      e.preventDefault()
      if (!options[i].disabled) onChange(options[i].value)
      return
    }
    const step = ({ ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 } as Record<string, number>)[e.key]
    if (!step || enabled.length === 0) return
    e.preventDefault()
    const at = enabled.indexOf(i)
    const target = enabled[(at + step + enabled.length) % enabled.length]
    onChange(options[target].value)
    refs.current[target]?.focus()
  }

  return (
    <div role="radiogroup" {...aria} className={cn('grid grid-cols-1 gap-2.5', COLS[columns], className)}>
      {options.map((o, i) => {
        const checked = o.value === value
        const titleId = `${uid}-${i}-t`
        const descId = `${uid}-${i}-d`
        const reasonId = `${uid}-${i}-r`
        return (
          <div
            key={o.value}
            ref={el => { refs.current[i] = el }}
            role="radio"
            aria-checked={checked}
            aria-disabled={o.disabled || undefined}
            aria-labelledby={titleId}
            aria-describedby={o.disabled && o.disabledReason ? `${descId} ${reasonId}` : descId}
            tabIndex={i === tabStop ? 0 : -1}
            onClick={() => { if (!o.disabled) onChange(o.value) }}
            onKeyDown={e => onKey(e, i)}
            className={cn(
              'relative flex min-w-0 flex-col gap-2 rounded-xl border bg-card p-3 text-left outline-none transition-colors',
              'focus-visible:ring-3 focus-visible:ring-ring/50',
              o.disabled ? 'cursor-not-allowed bg-muted/40' : 'cursor-pointer hover:border-foreground/30',
              checked && 'border-brand bg-brand/[0.06] ring-1 ring-brand/50',
            )}
          >
            {o.diagram && (
              <div aria-hidden="true" className={cn('flex min-h-8 items-center overflow-hidden rounded-md bg-muted/50 px-2 py-1.5', o.disabled && 'opacity-50')}>
                {o.diagram}
              </div>
            )}
            <div className="flex items-start justify-between gap-2">
              <span id={titleId} className={cn('text-sm font-medium', o.disabled && 'text-muted-foreground')}>{o.title}</span>
              {checked && <CheckIcon className="mt-0.5 size-4 shrink-0 text-brand" aria-hidden="true" />}
            </div>
            <p id={descId} className="text-xs text-foreground/70">{o.description}</p>
            {o.disabled && o.disabledReason && (
              <p id={reasonId} className="flex items-start gap-1.5 text-xs font-medium text-foreground">
                <ToneDot tone="warning" className="mt-1 size-1.5" />{o.disabledReason}
              </p>
            )}
          </div>
        )
      })}
    </div>
  )
}
