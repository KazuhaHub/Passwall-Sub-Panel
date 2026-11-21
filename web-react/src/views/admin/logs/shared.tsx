import { useCallback, type ReactNode } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'
import { CopyButton } from '@/components/common/CopyButton'
import { IconButton } from '@/components/common/IconButton'
import { IconClose } from '@/lib/icons'
import { PAGE_SIZE_OPTIONS } from '@/components/data/dataTableLogic'
import { spacedT } from '@/i18n/spacing'
import type { UrlCodec } from '@/hooks/useUrlState'
import { cn } from '@/lib/utils'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { formatRegion } from '@/utils/geo'
import type { GeoLocation } from '@/api/subLogs'

// Pieces shared by the three record lists this package owns (Logs, Sync tasks, Node issues). They all
// show server-paged records that an admin scans for one thing, and open the full record in a sheet.

/** The admin namespace's `t` with interpolation boundaries resolved, keys read `logs.…`, `sync_tasks.…`. */
export function useAdminT(): TFunction {
  const { t } = useTranslation('admin')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useCallback(spacedT(t), [t])
}

/**
 * The page size these lists start with. Before the per-list remembered view existed, the Users page and
 * these lists shared one `psp_page_size` key; honouring it keeps an admin's "100 per page" on first visit.
 * It is only read, never written: the list's own remembered view takes over once the size is changed.
 */
export function legacyPageSize(): number {
  try {
    const n = parseInt(localStorage.getItem('psp_page_size') ?? '', 10)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(n) ? n : 25
  } catch { return 25 }
}

/** `?row=<id>`: digits only, anything else is no selection. */
export function parseRowId(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const n = Number(raw)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}

/**
 * Server-paged lists are sorted by the server, so a column must not offer a sort of the loaded page: it
 * would reorder ten rows of a thousand and look like a sorted list.
 */
export function plainColumns<T>(columns: ColumnDef<T, any>[]): ColumnDef<T, any>[] {
  return columns.map(c => ({ ...c, enableSorting: false }))
}

/** A URL value that is one of `values`, or empty for "no filter"; anything else reads as empty. */
export function choiceCodec<V extends string>(values: readonly V[]): UrlCodec<V | ''> {
  return {
    parse: raw => (raw !== null && (values as readonly string[]).includes(raw) ? (raw as V) : ''),
    serialize: v => (v === '' ? null : v),
  }
}

/**
 * A facet that holds one value (the API takes one status, one method). FacetFilter is a checklist, so the
 * newly ticked box wins and unticking the only one clears it.
 */
export function singleFacet<V extends string>(current: V | '', set: (next: V | '') => void) {
  return {
    selected: current ? [current] : [],
    onChange: (next: string[]) => set((next.find(v => v !== current) ?? '') as V | ''),
  }
}

function localTime(value: string, tz: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  try { return d.toLocaleString(undefined, { timeZone: tz || undefined }) } catch { return d.toLocaleString() }
}

/**
 * A time in the panel's time zone (the one traffic resets and expiry use); the title carries the same
 * instant with the browser's zone beside it when the two differ, so the absolute time is one hover away.
 */
export function TimeCell({ value, className }: { value?: string | null; className?: string }) {
  const tz = useSiteStore(s => s.timezone)
  if (!value) return <span className={className}>—</span>
  return (
    <time dateTime={value} title={formatDualTz(value, tz)} className={cn('whitespace-nowrap tabular-nums', className)}>
      {localTime(value, tz)}
    </time>
  )
}

/** An address with the region the offline IP database estimates, when there is one. */
export function IpCell({ ip, region }: { ip?: string; region?: GeoLocation }) {
  const t = useAdminT()
  const label = formatRegion(region)
  return (
    <div className="min-w-0">
      <div className="truncate font-mono text-[0.8125rem]" title={ip}>{ip || '—'}</div>
      {label && <div className="truncate text-xs text-muted-foreground" title={`${label} (${t('logs.region_hint')})`}>{label}</div>}
    </div>
  )
}

/** Label / value rows for a sheet. */
export function DetailList({ children, className }: { children: ReactNode; className?: string }) {
  return <dl className={cn('grid grid-cols-[6.5rem_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm', className)}>{children}</dl>
}

export function DetailRow({ label, children, mono, hidden }: { label: ReactNode; children: ReactNode; mono?: boolean; hidden?: boolean }) {
  if (hidden) return null
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn('min-w-0 break-words', mono && 'font-mono text-[0.8125rem] break-all')}>{children}</dd>
    </>
  )
}

/** Long text, JSON and diagnostics: monospace, scrolls on its own, one click copies the whole thing. */
export function CodeSection({ title, text, name }: { title: ReactNode; text: string; name?: string }) {
  return (
    <section className="mt-5">
      <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</h3>
      <div className="relative rounded-md border bg-muted/40">
        <pre className="max-h-72 overflow-auto p-3 pr-10 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">{text}</pre>
        <div className="absolute top-1 right-1"><CopyButton value={text} name={name} size="icon-xs" /></div>
      </div>
    </section>
  )
}

/** Pretty-prints a JSON string; anything that is not JSON is shown as it is. */
export function formatJson(s?: string): string {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

/** The line in a filter bar that says how many records match. */
export function ResultCount({ total }: { total: number | undefined }) {
  const t = useAdminT()
  if (total === undefined) return null
  return <span className="text-sm text-muted-foreground tabular-nums" aria-live="polite">{t('logs.result_count', { count: total })}</span>
}

/** A row at phone width: a title, an optional status, and a few quiet lines. The card itself opens the sheet. */
export function RecordCard({ title, status, lines }: { title: ReactNode; status?: ReactNode; lines: ReactNode[] }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{title}</span>
        {status}
      </div>
      {lines.filter(Boolean).map((l, i) => <div key={i} className="truncate text-xs text-muted-foreground">{l}</div>)}
    </div>
  )
}

/** Cell classes: the column takes a share of the table and clips with an ellipsis; the full text is in `title`. */
export const FIT = 'max-w-0 overflow-hidden text-ellipsis whitespace-nowrap'

/** A removable "user: alice" filter, the shape the risk center's links arrive in. */
export function FilterChip({ label, onRemove, removeLabel }: { label: string; onRemove: () => void; removeLabel: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border bg-muted/50 py-0.5 pr-0.5 pl-2 text-sm">
      {label}
      <IconButton label={removeLabel} size="icon-xs" onClick={onRemove}><IconClose /></IconButton>
    </span>
  )
}
