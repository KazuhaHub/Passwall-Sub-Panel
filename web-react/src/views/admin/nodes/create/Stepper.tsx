import { CheckIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

/** The create page's progress: the steps in order, the current one marked for assistive technology too, the finished ones ticked. */
export function Stepper({ steps, active, label }: { steps: readonly string[]; active: number; label: string }) {
  return (
    <ol aria-label={label} className="mb-6 flex flex-wrap items-center gap-x-2 gap-y-2 text-sm">
      {steps.map((name, i) => (
        <li key={name} aria-current={i === active ? 'step' : undefined} className="flex items-center gap-2">
          <span className={cn('flex size-6 items-center justify-center rounded-full border text-xs tabular-nums',
            i < active && 'border-brand bg-brand text-brand-foreground', i === active && 'border-brand text-brand', i > active && 'text-muted-foreground')}>
            {i < active ? <CheckIcon className="size-3.5" aria-hidden="true" /> : i + 1}
          </span>
          <span className={cn(i === active ? 'font-medium' : 'text-muted-foreground')}>{name}</span>
          {i < steps.length - 1 && <span aria-hidden="true" className="mx-1 h-px w-6 bg-border" />}
        </li>
      ))}
    </ol>
  )
}
