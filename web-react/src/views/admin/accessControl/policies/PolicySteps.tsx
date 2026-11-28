import { useRef, type ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { useContainerWidth } from '../../rulesets/useContainerWidth'

/** From this width of the tab the steps are a list on the left and the chosen step on the right. */
export const SPLIT_MIN = 760

export interface StepEntry {
  id: string
  /** The step's number in the match order (pipelineSteps). */
  n: number
  /** Its name in the step list. */
  label: string
  /** How many entries it holds, when it holds a list (policies, rules, groups); none for a step that is a single fact. */
  count?: number
  /** The step itself, as the stacked rail draws it; `last` drops the line down to the next step. */
  render: (last: boolean) => ReactNode
}

/**
 * The match order, two ways. Wide enough: the steps in order on the left (number, name, count), the chosen step on the
 * right with its whole width, the way the rule sets page lists its groups beside the open one, so a step with many
 * policies does not push the rest of the order off the screen. Narrower (or not measured yet, as in tests): every step
 * stacked on one rail, top to bottom.
 */
export function PolicySteps({ entries, selected, onSelect, label }: {
  entries: readonly StepEntry[]
  selected: string
  onSelect: (id: string) => void
  /** The step list's accessible name. */
  label: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const width = useContainerWidth(ref)
  const split = width !== null && width >= SPLIT_MIN
  const current = entries.find(e => e.id === selected) ?? entries[0]
  return (
    <div ref={ref}>
      {split && current
        ? (
          <div className="grid grid-cols-[15rem_minmax(0,1fr)] items-start gap-6">
            <nav aria-label={label} className="sticky top-4 rounded-xl border bg-card p-1.5">
              <ol className="grid gap-0.5">
                {entries.map((e, i) => {
                  const active = e.id === current.id
                  return (
                    <li key={e.id} className="relative">
                      {i < entries.length - 1 && <span aria-hidden className="absolute top-7 -bottom-2 left-[1.3rem] w-0.5 bg-border" />}
                      <button type="button" aria-current={active ? 'step' : undefined} onClick={() => onSelect(e.id)} data-testid={`step-nav-${e.id}`}
                        className={cn('relative flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-sm outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50',
                          active && 'bg-segment-active font-semibold shadow-xs ring-1 ring-segment-border')}>
                        <span aria-hidden className={cn('flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold', active && 'bg-primary text-primary-foreground')}>{e.n}</span>
                        <span className="min-w-0 flex-1 truncate">{e.label}</span>
                        {e.count !== undefined && e.count > 0 && <span className="shrink-0 text-xs font-normal text-muted-foreground tabular-nums">{e.count}</span>}
                      </button>
                    </li>
                  )
                })}
              </ol>
            </nav>
            <div className="min-w-0">{current.render(true)}</div>
          </div>
        )
        : entries.map((e, i) => <div key={e.id}>{e.render(i === entries.length - 1)}</div>)}
    </div>
  )
}
