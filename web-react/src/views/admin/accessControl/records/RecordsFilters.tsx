import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import UserAutocomplete from '@/components/UserAutocomplete'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { IconFilter } from '@/lib/icons'
import { R } from './cells'
import { REC_DEFAULT_SINCE, REC_MAX_RANGE_DAYS, relativeSince, type RecordsFilter } from './recordsParams'

const HOUR = 3_600_000

export interface RuleOptions {
  policies: { source: string; label: string }[]
  rules: { source: string; label: string }[]
  allowlists: { source: string; label: string }[]
  deleted: { source: string }[]
}

/** A label above a control, no reserved message line: these are filters, not form fields. */
function FilterField({ label, htmlFor, className, children }: { label: string; htmlFor: string; className?: string; children: ReactNode }) {
  return (
    <div className={`grid min-w-0 gap-1 ${className ?? ''}`}>
      <Label htmlFor={htmlFor} className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

/**
 * The records tab's filter row: account, range, rule, node, exit server and the destination search. On a
 * phone the account and the range stay and the rest sit behind "more filters", with a count of what is
 * in force. Every control writes through `patch` (the URL) except the search, whose text is the tab's own.
 */
export function RecordsFilters({
  filter, patch, typed, onTyped, retention, ruleOptions, deletedRule, panels, showExit, exitOptions, exitName, filterCount, phone,
}: {
  filter: RecordsFilter
  patch: (next: Partial<RecordsFilter>) => void
  typed: string
  onTyped: (value: string) => void
  retention: number | undefined
  ruleOptions: RuleOptions
  deletedRule: (source: string) => string
  panels: { panel_id: number; panel_name: string }[]
  showExit: boolean
  exitOptions: number[]
  exitName: (id: number) => string
  filterCount: number
  phone: boolean
}) {
  const { t } = useTranslation(['admin'])
  const [more, setMore] = useState(false)
  const days7 = relativeSince(7, retention)

  const rangeOptions: { value: string; label: string }[] = [
    { value: '1h', label: t(`${R}range.h1`) },
    { value: '24h', label: t(`${R}range.h24`) },
    ...(!retention || retention > 1 ? [{ value: days7, label: t(`${R}range.days`, { days: days7.replace('d', '') }) }] : []),
  ]
  const custom = /^\d{4}-/.test(filter.since)
  const rangeDays = /^(\d+)d$/.exec(filter.since)?.[1]
  // A link may carry another window ("14d"): it is an option for as long as it is the current one.
  if (!custom && !rangeOptions.some(o => o.value === filter.since)) {
    rangeOptions.push({ value: filter.since, label: t(`${R}range.days`, { days: String(rangeDays ?? '') }) })
  }
  const rangeValue = custom ? 'custom' : filter.since
  const setRange = (v: string) => {
    if (v === 'custom') {
      // Start from the span of the current range, in browser time, as datetime-local text.
      const end = Date.now(); const start = end - 24 * HOUR
      const local = (ms: number) => new Date(ms - new Date(ms).getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
      patch({ since: local(start), until: local(end) })
    } else patch({ since: v, until: undefined })
  }

  const ruleKnown = new Set([...ruleOptions.policies, ...ruleOptions.rules, ...ruleOptions.allowlists, ...ruleOptions.deleted].map(o => o.source))
  const opts = (list: { source: string; label: string }[]) => list.map(o => <option key={o.source} value={o.source}>{o.label}</option>)

  const rule = (
    <FilterField label={t(`${R}filters.source`)} htmlFor="rec-rule">
      <NativeSelect id="rec-rule" value={filter.source ?? ''} onChange={e => patch({ source: e.target.value || undefined })}>
        <option value="">{t(`${R}filters.source_all`)}</option>
        {ruleOptions.policies.length > 0 && <optgroup label={t(`${R}filters.group_policies`)}>{opts(ruleOptions.policies)}</optgroup>}
        {ruleOptions.rules.length > 0 && <optgroup label={t(`${R}filters.group_rules`)}>{opts(ruleOptions.rules)}</optgroup>}
        {ruleOptions.allowlists.length > 0 && <optgroup label={t(`${R}filters.group_allowlists`)}>{opts(ruleOptions.allowlists)}</optgroup>}
        {ruleOptions.deleted.length > 0 && (
          <optgroup label={t(`${R}filters.group_deleted`)}>
            {ruleOptions.deleted.map(o => <option key={o.source} value={o.source}>{deletedRule(o.source)}</option>)}
          </optgroup>
        )}
        {/* A link may name a rule this range never saw: it stays selectable while it is the filter. */}
        {filter.source && !ruleKnown.has(filter.source) && <option value={filter.source}>{deletedRule(filter.source)}</option>}
      </NativeSelect>
    </FilterField>
  )
  const panel = (
    <FilterField label={t(`${R}filters.panel`)} htmlFor="rec-panel">
      <NativeSelect id="rec-panel" value={filter.panel_id ?? ''} onChange={e => patch({ panel_id: Number(e.target.value) || undefined })}>
        <option value="">{t(`${R}filters.panel_all`)}</option>
        {panels.map(n => <option key={n.panel_id} value={n.panel_id}>{n.panel_name}</option>)}
        {filter.panel_id !== undefined && !panels.some(n => n.panel_id === filter.panel_id) && <option value={filter.panel_id}>{`#${filter.panel_id}`}</option>}
      </NativeSelect>
    </FilterField>
  )
  const exit = showExit && (
    <FilterField label={t(`${R}filters.exit`)} htmlFor="rec-exit">
      <NativeSelect id="rec-exit" value={filter.exit_panel_id === undefined ? '' : String(filter.exit_panel_id)}
        onChange={e => patch({ exit_panel_id: e.target.value === '' ? undefined : Number(e.target.value) })}>
        <option value="">{t(`${R}filters.exit_all`)}</option>
        <option value="0">{t(`${R}filters.exit_direct`)}</option>
        {exitOptions.map(id => <option key={id} value={id}>{exitName(id)}</option>)}
      </NativeSelect>
    </FilterField>
  )
  const search = (
    <FilterField label={t(`${R}filters.search`)} htmlFor="rec-search">
      <Input id="rec-search" type="search" value={typed} onChange={e => onTyped(e.target.value)} autoComplete="off" spellCheck={false} />
    </FilterField>
  )
  const extras = <>{rule}{panel}{exit}{search}</>

  return (
    <div className="mb-3 grid gap-3">
      <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[repeat(auto-fit,minmax(11rem,1fr))]">
        <div className="grid min-w-0 gap-1">
          <span className="text-xs text-muted-foreground">{t(`${R}filters.user`)}</span>
          <UserAutocomplete value={filter.user_id ?? null} onChange={id => patch({ user_id: id ?? undefined })}
            label={t(`${R}filters.user`)} fullWidth />
        </div>
        <FilterField label={t(`${R}filters.range`)} htmlFor="rec-range">
          <NativeSelect id="rec-range" value={rangeValue} onChange={e => setRange(e.target.value)}>
            {rangeOptions.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            <option value="custom">{t(`${R}range.custom`)}</option>
          </NativeSelect>
        </FilterField>
        {phone
          ? (
            <div>
              <Button type="button" variant="outline" aria-expanded={more} onClick={() => setMore(m => !m)}>
                <IconFilter />{t(`${R}filters.more`)}
                {filterCount > 0 && <Badge variant="secondary" className="tabular-nums">{filterCount}</Badge>}
              </Button>
            </div>
          )
          : extras}
      </div>
      {phone && more && <div className="grid gap-3">{extras}</div>}
      {custom && (
        <div className="flex flex-wrap items-end gap-3">
          <FilterField label={t(`${R}range.since`)} htmlFor="rec-since">
            <Input id="rec-since" type="datetime-local" value={filter.since.slice(0, 16)}
              onChange={e => patch({ since: e.target.value || REC_DEFAULT_SINCE, until: filter.until })} />
          </FilterField>
          <FilterField label={t(`${R}range.until`)} htmlFor="rec-until">
            <Input id="rec-until" type="datetime-local" value={(filter.until ?? '').slice(0, 16)}
              onChange={e => patch({ since: filter.since, until: e.target.value || undefined })} />
          </FilterField>
          <p className="text-xs text-muted-foreground">{t(`${R}range.custom_note`, { days: REC_MAX_RANGE_DAYS })}</p>
        </div>
      )}
    </div>
  )
}
