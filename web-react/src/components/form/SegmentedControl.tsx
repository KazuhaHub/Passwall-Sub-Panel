import { useRef, type KeyboardEvent, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface SegmentOption<V extends string> {
  value: V
  label: ReactNode
  icon?: ReactNode
  disabled?: boolean
}

export interface SegmentedControlProps<V extends string> {
  value: V
  onChange: (next: V) => void
  options: readonly SegmentOption<V>[]
  'aria-label'?: string
  'aria-labelledby'?: string
  size?: 'sm' | 'default'
  disabled?: boolean
  className?: string
}

/**
 * Two to four mutually exclusive choices shown side by side (a view switch, a
 * mode). A radio group, not a tab list: it picks a value, it does not reveal a
 * panel. One tab stop; the arrow keys move and select, skipping disabled
 * options and wrapping at the ends.
 */
export function SegmentedControl<V extends string>({
  value, onChange, options, size = 'default', disabled, className, ...aria
}: SegmentedControlProps<V>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const enabled = options.map((o, i) => (o.disabled || disabled ? -1 : i)).filter(i => i >= 0)
  const current = options.findIndex(o => o.value === value)
  const tabStop = enabled.includes(current) ? current : enabled[0]

  const move = (e: KeyboardEvent, from: number) => {
    const keys: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }
    let target: number | undefined
    if (e.key in keys) {
      const at = enabled.indexOf(from)
      if (at < 0 || enabled.length === 0) return
      target = enabled[(at + keys[e.key] + enabled.length) % enabled.length]
    } else if (e.key === 'Home') target = enabled[0]
    else if (e.key === 'End') target = enabled[enabled.length - 1]
    if (target === undefined) return
    e.preventDefault()
    onChange(options[target].value)
    refs.current[target]?.focus()
  }

  return (
    <div
      role="radiogroup"
      {...aria}
      className={cn('inline-flex w-fit max-w-full items-center gap-0.5 rounded-lg bg-muted p-0.5', className)}
    >
      {options.map((o, i) => {
        const checked = o.value === value
        const off = o.disabled || disabled
        return (
          <button
            key={o.value}
            ref={el => { refs.current[i] = el }}
            type="button"
            role="radio"
            aria-checked={checked}
            aria-disabled={off || undefined}
            tabIndex={i === tabStop ? 0 : -1}
            onClick={() => { if (!off) onChange(o.value) }}
            onKeyDown={e => move(e, i)}
            className={cn(
              'inline-flex items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
              size === 'sm' ? 'h-[calc(var(--control-h)-0.75rem)] px-2.5 text-xs' : 'h-[calc(var(--control-h)-0.375rem)]',
              checked ? 'bg-background text-foreground shadow-sm dark:bg-input/60' : 'text-muted-foreground hover:text-foreground',
              off && 'cursor-not-allowed opacity-50 hover:text-muted-foreground',
              '[&_svg]:size-4 [&_svg]:shrink-0',
            )}
          >
            {o.icon}
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
