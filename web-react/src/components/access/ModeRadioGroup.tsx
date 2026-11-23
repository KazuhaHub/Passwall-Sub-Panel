import { useId, type ReactNode } from 'react'

import { cn } from '@/lib/utils'

export interface ModeRadioOption<V extends string> {
  value: V
  /** The option's words, with whatever explains it under them. */
  children: ReactNode
  disabled?: boolean
}

/**
 * A short list of one-of-N choices where each choice is a paragraph, not a word (a recording mode and what
 * it costs, a hostname-matching mode and what it does). Native radios, so the browser's arrow keys and the
 * accessible group come for free; the chosen row is outlined in the brand colour.
 */
export function ModeRadioGroup<V extends string>({ value, onChange, options, 'aria-label': label, className }: {
  /** null: nothing chosen yet. */
  value: V | null
  onChange: (next: V) => void
  options: readonly ModeRadioOption<V>[]
  'aria-label': string
  className?: string
}) {
  const name = useId()
  return (
    <div role="radiogroup" aria-label={label} className={cn('grid gap-2', className)}>
      {options.map(o => (
        <label
          key={o.value}
          className={cn(
            'flex items-start gap-2.5 rounded-lg border p-2.5 text-sm',
            value === o.value ? 'border-brand bg-brand/[0.06] ring-1 ring-brand/50' : 'border-border',
            o.disabled ? 'cursor-not-allowed text-muted-foreground' : 'cursor-pointer hover:border-foreground/30',
          )}
        >
          <input
            type="radio" name={name} value={o.value} checked={value === o.value} disabled={o.disabled}
            onChange={() => onChange(o.value)} className="mt-1 size-4 shrink-0 accent-[var(--brand)]"
          />
          <span className="min-w-0">{o.children}</span>
        </label>
      ))}
    </div>
  )
}
