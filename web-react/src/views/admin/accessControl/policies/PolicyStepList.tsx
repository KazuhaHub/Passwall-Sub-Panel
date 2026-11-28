import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconChevronDown, IconChevronRight, IconSearch } from '@/lib/icons'

/** Up to this many policies a step is a plain list; past it the step can be filtered and folded. */
export const STEP_FOLD_AT = 6

/**
 * The rows of one rail step. A short step is just its rows. A long one gets a filter box (the policy's name, its lists and
 * its scope, matched on this page only) and a fold: folding hides the rows, never reorders them. Each row keeps its place in
 * the whole step (`index`, `count`), because the up/down order is the step's, not the filtered view's.
 */
export default function PolicyStepList({ label, rows, searchText, renderRow, forceOpen }: {
  /** Names the step for the filter and the fold buttons. */
  label: string
  rows: DestPolicy[]
  searchText: (p: DestPolicy) => string
  renderRow: (p: DestPolicy, index: number, count: number) => ReactNode
  /** A row of this step has been asked for (a highlight): the step is open and unfiltered. */
  forceOpen: boolean
}) {
  const { t } = useTranslation(['admin'])
  const P = 'admin:access_control.policies.rail.'
  const [q, setQ] = useState('')
  const [folded, setFolded] = useState(false)
  if (rows.length === 0) return null
  const long = rows.length > STEP_FOLD_AT
  const needle = q.trim().toLowerCase()
  const open = !long || !folded || forceOpen
  const visible = long && needle && !forceOpen ? rows.filter(p => searchText(p).toLowerCase().includes(needle)) : rows
  const list = (
    <div className="grid gap-2">
      {visible.map(p => renderRow(p, rows.indexOf(p), rows.length))}
    </div>
  )
  if (!long) return list
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-40 max-w-72 flex-1">
          <IconSearch aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" value={q} className="pl-8" aria-label={t(`${P}filter_label`, { step: label })} placeholder={t(`${P}filter_placeholder`)}
            onChange={e => { setQ(e.target.value); if (e.target.value) setFolded(false) }} />
        </div>
        {needle && <span role="status" className="text-xs text-muted-foreground tabular-nums">{t(`${P}shown_of`, { shown: visible.length, total: rows.length })}</span>}
        <Button variant="ghost" size="sm" className="ml-auto" aria-expanded={open} onClick={() => setFolded(f => !f)}>
          {open ? <IconChevronDown /> : <IconChevronRight />}
          {open ? t(`${P}fold`, { step: label }) : t(`${P}unfold`, { step: label, count: rows.length })}
        </Button>
      </div>
      {open && (visible.length > 0 ? list : <p className="text-sm text-muted-foreground">{t(`${P}filter_none`, { q: q.trim() })}</p>)}
    </div>
  )
}
