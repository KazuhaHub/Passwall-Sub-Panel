import { useState } from 'react'
import { ChevronDownIcon, ListFilterIcon, XIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'
import { TipButton } from './TipButton'
import { TOUCH_HEIGHT } from './touchTarget'

export interface FacetOption {
  value: string
  label: string
  /** How many rows each option would match; shown beside it so an empty filter is visible before it is chosen. */
  count?: number
}

export interface FacetFilterProps {
  label: string
  options: readonly FacetOption[]
  selected: readonly string[]
  onChange: (next: string[]) => void
  /** Show a search box when there are more options than this. */
  searchAbove?: number
  className?: string
}

/**
 * A filter button that opens a checklist. The button carries the selection
 * (the one option chosen, or "first and N more"), so a filtered list says so
 * without opening anything, and a separate x clears it in one click.
 */
export function FacetFilter({ label, options, selected, onChange, searchAbove = 8, className }: FacetFilterProps) {
  const { t } = useTranslation('common')
  const [q, setQ] = useState('')
  const shown = q.trim() ? options.filter(o => o.label.toLowerCase().includes(q.trim().toLowerCase())) : options
  const toggle = (v: string) => onChange(selected.includes(v) ? selected.filter(x => x !== v) : [...selected, v])
  const labelOf = (v: string) => options.find(o => o.value === v)?.label ?? v

  const summary = selected.length === 0
    ? null
    : selected.length === 1
      ? labelOf(selected[0])
      : t('ui.data.facet_more', { first: labelOf(selected[0]), count: selected.length, rest: selected.length - 1 })

  return (
    <div data-slot="facet-filter" className={cn('inline-flex items-center', className)}>
      <Popover onOpenChange={open => { if (!open) setQ('') }}>
        <PopoverTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              className={cn(TOUCH_HEIGHT, selected.length > 0 && 'rounded-r-none border-brand/60 bg-brand/5 dark:bg-brand/10')}
            />
          }
        >
          <ListFilterIcon aria-hidden="true" />
          {label}
          {summary && (
            <span className="max-w-40 truncate rounded-full bg-brand px-1.5 text-[11px] leading-4 text-brand-foreground">{summary}</span>
          )}
          <ChevronDownIcon className="opacity-60" aria-hidden="true" />
        </PopoverTrigger>
        <PopoverContent align="start" className="w-64 gap-2 p-2">
          {options.length > searchAbove && (
            <Input
              type="search"
              role="searchbox"
              aria-label={t('ui.data.facet_search')}
              value={q}
              onChange={e => setQ(e.target.value)}
              placeholder={t('ui.data.facet_search')}
              className="h-8"
            />
          )}
          <div className="max-h-64 overflow-auto">
            {shown.length === 0 && <p className="px-2 py-1.5 text-sm text-muted-foreground">{t('ui.data.facet_none')}</p>}
            {shown.map(o => (
              <label key={o.value} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted">
                <Checkbox checked={selected.includes(o.value)} onCheckedChange={() => toggle(o.value)} />
                <span className="min-w-0 flex-1 truncate">{o.label}</span>
                {o.count !== undefined && <span className="text-xs text-muted-foreground tabular-nums">{o.count}</span>}
              </label>
            ))}
          </div>
        </PopoverContent>
      </Popover>
      {selected.length > 0 && (
        <TipButton
          label={t('ui.data.facet_clear_named', { label })}
          variant="outline"
          size="icon-sm"
          className="rounded-l-none border-l-0 border-brand/60 bg-brand/5 dark:bg-brand/10"
          onClick={() => onChange([])}
        >
          <XIcon />
        </TipButton>
      )}
    </div>
  )
}
