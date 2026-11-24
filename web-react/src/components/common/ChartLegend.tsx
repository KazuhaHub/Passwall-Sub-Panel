import { useCallback, useState } from 'react'
import { cn } from '@/lib/utils'
import { DASHES, type Dash } from '@/lib/chartKit'

export interface ChartLegendItem { name: string; color: string; dash?: Dash }

/**
 * The legend of a chart, as plain HTML in the app's style (the ECharts legend is turned off). One button per series: a
 * short line sample in the series colour AND pattern (solid, dashed, dotted: the same as on the chart, so a series is
 * never told by colour alone) and the name in muted text. Pressing it hides or shows that series (`aria-pressed`);
 * a hidden one is dimmed. Items wrap on a narrow screen.
 */
export function ChartLegend({ items, hidden, onToggle, className }: {
  items: readonly ChartLegendItem[]
  hidden: ReadonlySet<string>
  onToggle: (name: string) => void
  className?: string
}) {
  return (
    <ul data-slot="chart-legend" className={cn('flex flex-wrap items-center justify-center gap-x-4 gap-y-1', className)}>
      {items.map((item, i) => {
        const off = hidden.has(item.name)
        return (
          <li key={item.name}>
            <button
              type="button"
              aria-pressed={!off}
              onClick={() => onToggle(item.name)}
              className={cn('inline-flex min-h-7 items-center gap-2 rounded-md px-1.5 text-xs text-muted-foreground outline-none transition-opacity hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50', off && 'opacity-45')}
            >
              <span aria-hidden="true" data-slot="chart-swatch" data-dash={item.dash ?? DASHES[i % DASHES.length]}
                className="inline-block h-0 w-4 shrink-0 border-t-2" style={{ borderTopStyle: item.dash ?? DASHES[i % DASHES.length], borderTopColor: item.color }} />
              <span>{item.name}</span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

/** The set of series the reader has hidden, and the toggle for it. */
export function useHiddenSeries() {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set())
  const toggle = useCallback((name: string) => setHidden(prev => {
    const next = new Set(prev)
    if (next.has(name)) next.delete(name); else next.add(name)
    return next
  }), [])
  return { hidden, toggle }
}
