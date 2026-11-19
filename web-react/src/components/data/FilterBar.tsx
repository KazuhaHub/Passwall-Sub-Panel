import { useEffect, useRef, useState, type ReactNode } from 'react'
import { SearchIcon, XIcon } from 'lucide-react'
import { useSpacedT } from '@/lib/cjkSpacing'
import { Button } from '@/components/ui/button'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { cn } from '@/lib/utils'
import { FacetFilter, type FacetOption } from './FacetFilter'
import { TipButton } from './TipButton'
import { TOUCH_TARGET } from './touchTarget'

export interface FilterBarFacet {
  id: string
  label: string
  options: readonly FacetOption[]
  selected: readonly string[]
  onChange: (next: string[]) => void
}

export interface FilterBarProps {
  /** Names the search landmark when a page has more than one; defaults to "筛选". */
  'aria-label'?: string
  search?: {
    value: string
    onChange: (next: string) => void
    placeholder?: string
    /** Quiet time before a typed keyword is reported. Default 250 ms; Enter reports at once. */
    debounceMs?: number
  }
  facets?: readonly FilterBarFacet[]
  /** Reset the keyword and every facet. The button shows only while something is filtered. */
  onClearAll?: () => void
  /** Anything else for the row (a date range, a view switch). */
  children?: ReactNode
  className?: string
}

/**
 * The top row of a list (plan 3.2): search box, one facet button per
 * dimension, "clear all". The keyword is debounced here, not by every page, so
 * a server-paged list is asked once per pause in typing. While typing, the box
 * shows what was typed even though the page still holds the old keyword.
 */
export function FilterBar({ search, facets = [], onClearAll, children, className, ...aria }: FilterBarProps) {
  const { t } = useSpacedT()
  const [draft, setDraft] = useState(search?.value ?? '')
  const reported = useRef(search?.value ?? '')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const onChange = useRef(search?.onChange)
  onChange.current = search?.onChange

  // A keyword changed from outside (Back, "clear all", a shared link) replaces the box.
  const external = search?.value
  useEffect(() => {
    if (external !== undefined && external !== reported.current) {
      reported.current = external
      setDraft(external)
    }
  }, [external])
  useEffect(() => () => clearTimeout(timer.current), [])

  const report = (v: string) => {
    clearTimeout(timer.current)
    if (v === reported.current) return
    reported.current = v
    onChange.current?.(v)
  }
  const type = (v: string) => {
    setDraft(v)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => report(v), search?.debounceMs ?? 250)
  }

  const filtered = (search?.value ?? '') !== '' || draft !== '' || facets.some(f => f.selected.length > 0)

  return (
    <div role="search" aria-label={aria['aria-label'] ?? t('ui.data.filter_bar')} data-slot="filter-bar" className={cn('flex flex-wrap items-center gap-2', className)}>
      {search && (
        <InputGroup className="w-full sm:w-64">
          <InputGroupAddon><SearchIcon aria-hidden="true" /></InputGroupAddon>
          <InputGroupInput
            type="search"
            role="searchbox"
            aria-label={search.placeholder ?? t('ui.data.search_placeholder')}
            placeholder={search.placeholder ?? t('ui.data.search_placeholder')}
            value={draft}
            onChange={e => type(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') report(draft) }}
            // Native search inputs draw their own clear control; ours has a tooltip and a label.
            className="[&::-webkit-search-cancel-button]:hidden"
          />
          {draft && (
            <InputGroupAddon align="inline-end">
              <TipButton
                label={t('ui.data.search_clear')}
                size="icon-xs"
                onClick={() => { setDraft(''); report('') }}
              >
                <XIcon />
              </TipButton>
            </InputGroupAddon>
          )}
        </InputGroup>
      )}
      {facets.map(f => (
        <FacetFilter key={f.id} label={f.label} options={f.options} selected={f.selected} onChange={f.onChange} />
      ))}
      {children}
      {filtered && onClearAll && (
        <Button variant="ghost" size="sm" className={TOUCH_TARGET} onClick={() => { setDraft(''); reported.current = ''; clearTimeout(timer.current); onClearAll() }}>
          {t('ui.data.clear_all')}
        </Button>
      )}
    </div>
  )
}
