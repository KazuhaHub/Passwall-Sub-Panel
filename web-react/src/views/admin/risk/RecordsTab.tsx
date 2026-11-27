import { Fragment, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { isAxiosError } from 'axios'
import type { ColumnDef } from '@tanstack/react-table'

import type { GeoEvidence } from '@/api/geoAnomalies'
import { FLAG_EVENTS, FLAG_LEVELS, type FlagRecord, type FlagRecordParams } from '@/api/riskCenter'
import { RISK_KINDS } from '@/api/riskSignals'
import { IconButton } from '@/components/common/IconButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { DataTable } from '@/components/data/DataTable'
import { DetailDialog } from '@/components/data/DetailDialog'
import { useRecordNav } from '@/components/data/useRecordNav'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Field } from '@/components/form/Field'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { IconFilter, IconSearch } from '@/lib/icons'
import { useFlagRecords } from '@/query/riskCenter'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import {
  FLAG_SOURCES, flagEventKey, flagLevelKey, flagSourceKey, flagText, userLabel, withNote,
} from '@/utils/riskCenter'
import { plainColumns } from '../logs/shared'
import { GeoDistance, GeoPlaces } from './evidence/GeoEvidence'
import { RiskKindEvidence } from './evidence/RiskEvidence'
import { paramRows } from './recordValues'
import { parseRecordsParams, RECORDS_PAGE_SIZES, recordsSearch, type RecordsFilters } from './riskParams'
import { useRiskT } from './useRiskT'

/**
 * The event badge's tone, by the level the record moved TO: into a flag or a suspension is the alarm,
 * into suspect the ramp, and every leave or lift (level none) is neutral — a cleared flag is news, not
 * good news. A review record has no level and is neutral too. The drawer's 时间线 tones its badges the
 * same way.
 */
export function levelTone(level: string): Tone {
  switch (level) {
    case 'flagged':
    case 'suspended':
      return 'danger'
    case 'suspect':
      return 'warning'
    default:
      return 'neutral'
  }
}

/** A datetime-local value (browser time) as the RFC 3339 instant the server
 *  takes; '' (or a half-typed value) as no bound. */
function toInstant(local: string | undefined): string | undefined {
  if (!local) return undefined
  const ms = Date.parse(local)
  return Number.isNaN(ms) ? undefined : new Date(ms).toISOString()
}

function isRiskKind(source: string): boolean {
  return (RISK_KINDS as readonly string[]).includes(source)
}

export interface RecordsTabProps {
  onOpenUser?: (userId: number) => void
}

/**
 * 记录: each time an account's concurrent-location verdict or a risk signal
 * entered or left suspect or flagged, each automatic suspension applied or
 * lifted, and each admin dismiss or trust action, newest first, with the
 * reason AT THE TIME.
 *
 * Every filter is the server's and part of the query key, so a page is a
 * page of the filtered records. The bar holds what an admin filters by most —
 * who, which source, when; level and change sit behind 更多筛选, with a badge
 * counting the ones set, so a filter in force is never out of sight.
 *
 * The tab keeps its filters in the URL (`rec_*`, riskParams), so opening an
 * account, following a drawer link and coming Back, a reload or a copied
 * link shows the same filtered page; written by REPLACE, like every filter on
 * the page. The drawer's 时间线 links here filtered to its account, and lists
 * that account's records itself in a form made for a side panel.
 */
export default function RecordsTab({ onOpenUser }: RecordsTabProps) {
  const { t, i18n } = useRiskT()
  const scope = useQueryScope()
  const [urlParams, setUrlParams] = useSearchParams()
  const filters = parseRecordsParams(urlParams)
  // recordsSearch starts any filter change again at the first page: page 3 of one filter is not a page of
  // another.
  const update = (patch: Partial<RecordsFilters>) => {
    setUrlParams(prev => recordsSearch(prev, patch), { replace: true })
  }
  // The record open in the dialog is the page's own, not the URL's: nothing about a record goes in the
  // address beyond the filters it already holds.
  const [openId, setOpenId] = useState<number | null>(null)

  const who = filters.user_id
  const sinceAt = toInstant(filters.since)
  const untilAt = toInstant(filters.until)
  const params: FlagRecordParams = {
    page: filters.page, page_size: filters.page_size,
    ...(who ? { user_id: who } : {}),
    ...(filters.source ? { source: filters.source } : {}),
    ...(filters.level ? { level: filters.level } : {}),
    ...(filters.event ? { event: filters.event } : {}),
    ...(sinceAt ? { since: sinceAt } : {}),
    ...(untilAt ? { until: untilAt } : {}),
  }
  const { data, isPending, error, refetch } = useFlagRecords(scope, params)
  const rows = data?.items
  const hidden = (filters.level ? 1 : 0) + (filters.event ? 1 : 0)
  const narrowed = !!(who || filters.source || filters.level || filters.event || filters.since || filters.until)
  const record = openId === null ? undefined : rows?.find(r => r.id === openId)
  // Previous / next walk the records on this page, in the order the table shows them.
  const rowIds = useMemo(() => (rows ?? []).map(r => r.id), [rows])
  const nav = useRecordNav(rowIds, openId, setOpenId)
  const err = error
    ? (isAxiosError(error) ? String(error.response?.data?.error ?? error.message) : String(error))
    : ''
  // datetime-local reads and writes the BROWSER's time, while every time the page prints is the
  // panel's: the bounds say which one they are.
  const browserTime = t('admin:risk_center.flags.browser_time')
  const language = i18n.language
  const panelTz = useSiteStore(s => s.timezone)

  const columns = useMemo<ColumnDef<FlagRecord, any>[]>(() => plainColumns<FlagRecord>([
    { id: 'at', header: t('admin:risk_center.flags.col_at'), meta: { label: t('admin:risk_center.flags.col_at'), hideable: false, className: 'w-44 text-xs whitespace-nowrap' },
      cell: c => <AtCell ms={c.row.original.at_ms} /> },
    { id: 'user', header: t('admin:risk_center.flags.col_user'), meta: { label: t('admin:risk_center.flags.col_user'), className: 'w-[16%] max-w-0 overflow-hidden text-ellipsis whitespace-nowrap' },
      cell: c => <span title={userLabel(c.row.original)}>{userLabel(c.row.original)}</span> },
    { id: 'source', header: t('admin:risk_center.flags.col_source'), meta: { label: t('admin:risk_center.flags.col_source'), className: 'w-32' },
      cell: c => <Badge variant="outline">{t(flagSourceKey(c.row.original.source), { defaultValue: c.row.original.source })}</Badge> },
    { id: 'event', header: t('admin:risk_center.flags.col_event'), meta: { label: t('admin:risk_center.flags.col_event'), className: 'w-40' },
      cell: c => <StatusBadge tone={levelTone(c.row.original.level)} label={t(flagEventKey(c.row.original.event), { defaultValue: c.row.original.event })} /> },
    // The reason AT THE TIME, from the numbers stored with the record — not the account's current verdict,
    // which may have moved since.
    { id: 'detail', header: t('admin:risk_center.flags.col_detail'), meta: { label: t('admin:risk_center.flags.col_detail'), className: 'max-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-xs text-muted-foreground' },
      cell: c => { const text = flagText(c.row.original, t, language); return <span title={text}>{text}</span> } },
    { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('admin:risk_center.flags.col_actions'), className: 'w-12' },
      cell: c => onOpenUser ? (
        <IconButton label={t('admin:risk_center.open_user')} size="icon-xs" onClick={() => onOpenUser(c.row.original.user_id)}><IconSearch /></IconButton>
      ) : null },
  ]), [t, language, onOpenUser])

  const clearAll = () => update({ user_id: undefined, source: undefined, since: undefined, until: undefined, level: undefined, event: undefined })

  const toolbar = (
    <div className="flex flex-wrap items-end gap-2">
      <UserAutocomplete value={filters.user_id ?? null} onChange={id => update({ user_id: id ?? undefined })}
        label={t('admin:risk_center.flags.col_user')} />
      <Field label={t('admin:risk_center.flags.filter_source')} className="w-44">
        <NativeSelect value={filters.source ?? ''} onChange={e => update({ source: e.target.value || undefined })}>
          <option value="">{t('admin:risk_center.live.opt_all')}</option>
          {FLAG_SOURCES.map(s => <option key={s} value={s}>{t(flagSourceKey(s))}</option>)}
        </NativeSelect>
      </Field>
      <Field label={withNote(t('admin:risk_center.flags.filter_since'), browserTime, i18n.language)}>
        <Input type="datetime-local" className="w-56" value={filters.since ?? ''} onChange={e => update({ since: e.target.value || undefined })} />
      </Field>
      <Field label={withNote(t('admin:risk_center.flags.filter_until'), browserTime, i18n.language)}>
        <Input type="datetime-local" className="w-56" value={filters.until ?? ''} onChange={e => update({ until: e.target.value || undefined })} />
      </Field>
      <Popover>
        <PopoverTrigger render={<Button variant="outline" />}>
          <IconFilter />
          {t('admin:risk_center.flags.more_filters')}
          {hidden > 0 && <Badge variant="secondary" className="tabular-nums">{hidden}</Badge>}
        </PopoverTrigger>
        <PopoverContent align="start" className="flex w-64 flex-col gap-3">
          <Field label={t('admin:risk_center.flags.filter_level')}>
            <NativeSelect value={filters.level ?? ''} onChange={e => update({ level: e.target.value || undefined })}>
              <option value="">{t('admin:risk_center.live.opt_all')}</option>
              {FLAG_LEVELS.map(l => <option key={l} value={l}>{t(flagLevelKey(l === 'cleared' ? '' : l))}</option>)}
            </NativeSelect>
          </Field>
          <Field label={t('admin:risk_center.flags.filter_event')}>
            <NativeSelect value={filters.event ?? ''} onChange={e => update({ event: e.target.value || undefined })}>
              <option value="">{t('admin:risk_center.live.opt_all')}</option>
              {FLAG_EVENTS.map(ev => <option key={ev} value={ev}>{t(flagEventKey(ev))}</option>)}
            </NativeSelect>
          </Field>
        </PopoverContent>
      </Popover>
      {narrowed && <Button variant="outline" onClick={clearAll}>{t('admin:risk_center.queue.clear_filters')}</Button>}
    </div>
  )

  const empty = narrowed
    ? <EmptyState compact title={t('admin:risk_center.flags.empty')} action={<Button variant="outline" size="sm" onClick={clearAll}>{t('admin:risk_center.queue.clear_filters')}</Button>} />
    : <EmptyState compact title={t('admin:risk_center.flags.empty')} />

  return (
    <div className="flex flex-col gap-3">
      <DataTable<FlagRecord>
        tableId="admin.risk.records"
        aria-label={t('admin:risk_center.tab_records')}
        mode="server"
        data={rows ?? []}
        total={data?.total}
        columns={columns}
        getRowId={r => String(r.id)}
        loading={isPending}
        error={err ? t('admin:risk_center.load_failed', { error: err }) : null}
        onRetry={() => void refetch()}
        page={filters.page}
        onPageChange={p => update({ page: p })}
        pageSize={filters.page_size}
        onPageSizeChange={n => update({ page_size: n })}
        pageSizeOptions={RECORDS_PAGE_SIZES}
        columnMenu={false}
        maxHeight={null}
        onRowOpen={r => setOpenId(r.id)}
        activeRowId={openId === null ? null : String(openId)}
        renderCard={r => (
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{userLabel(r)}</span>
              <StatusBadge tone={levelTone(r.level)} label={t(flagEventKey(r.event), { defaultValue: r.event })} />
            </div>
            <div className="text-xs text-muted-foreground"><AtCell ms={r.at_ms} /></div>
            <div className="text-xs text-muted-foreground">{flagText(r, t, language)}</div>
          </div>
        )}
        toolbar={toolbar}
        empty={empty}
      />
      <DetailDialog
        open={record !== undefined}
        size="lg"
        nav={nav}
        onClose={() => setOpenId(null)}
        title={record ? userLabel(record) : ''}
        status={record && <StatusBadge tone={levelTone(record.level)} label={t(flagEventKey(record.event), { defaultValue: record.event })} />}
        description={record && `${t(flagSourceKey(record.source), { defaultValue: record.source })} · ${formatMsDualTz(record.at_ms, panelTz)}`}
        actions={record && onOpenUser && (
          <Button variant="outline" size="sm" onClick={() => onOpenUser(record.user_id)}>{t('admin:risk_center.open_user')}</Button>
        )}
      >
        {record && <RecordDetail rec={record} />}
      </DetailDialog>
    </div>
  )
}

/** The record's time in panel time, exact (the dialog and the title carry the zone). */
function AtCell({ ms }: { ms: number }) {
  const panelTz = useSiteStore(s => s.timezone)
  return <span>{formatMsDualTz(ms, panelTz)}</span>
}

/**
 * One record, expanded: its sentence, then its numbers the way the rest of
 * the page shows them — a geo record's places and distance, a risk record's
 * evidence panel for its kind, and every named value as a row (paramRows:
 * labels, tiers, hold names, panel-time instants, never a raw code). The
 * params as sent are one click further, under 原始数据, for the case the
 * rows do not cover.
 *
 * The drawer's 时间线 expands its entries with it too, `sentence` off: its
 * entry already reads the sentence on the line above.
 */
export function RecordDetail({ rec, sentence = true }: { rec: FlagRecord; sentence?: boolean }) {
  const { t, i18n } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const [raw, setRaw] = useState(false)
  const params = rec.params && typeof rec.params === 'object' ? rec.params as Record<string, unknown> : {}
  const geoEvidence = rec.source === 'geo' && params.evidence && typeof params.evidence === 'object'
    ? params.evidence as GeoEvidence
    : undefined
  // A risk record's params ARE its verdict's evidence, drawn by its kind's panel; laid out as rows as
  // well, they would say the same thing twice.
  const risk = isRiskKind(rec.source)
  const rows = risk ? [] : paramRows(rec, t, panelTz, i18n.language)
  const hasParams = rec.params !== null && rec.params !== undefined
  return (
    <div data-testid="record-detail" className="flex flex-col gap-3 py-1 text-[13px]">
      {sentence && <p>{flagText(rec, t, i18n.language)}</p>}
      {geoEvidence && (
        <div>
          <GeoPlaces row={{ places: [], evidence: geoEvidence }} />
          <GeoDistance evidence={geoEvidence} />
        </div>
      )}
      {risk && (
        <div className="flex flex-col gap-1.5">
          <RiskKindEvidence kind={rec.source} evidence={rec.params} userId={rec.user_id} />
        </div>
      )}
      {rows.length > 0 && (
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
          {rows.map(r => (
            <Fragment key={r.key}>
              <dt className="text-muted-foreground">{r.label}</dt>
              <dd>{r.value}</dd>
            </Fragment>
          ))}
        </dl>
      )}
      {hasParams && (
        <div>
          <Button variant="outline" size="sm" className="-ml-2" onClick={() => setRaw(v => !v)} aria-expanded={raw}>
            {raw ? t('admin:risk_center.flags.raw_hide') : t('admin:risk_center.flags.raw_show')}
          </Button>
        </div>
      )}
      {/* Address-free: no producer writes an address into params (pinned server-side by JSON-walk tests),
          and a review record carries the admin's id and levels only — never a name, never the note. */}
      {raw && <pre className="font-mono text-xs break-all whitespace-pre-wrap">{JSON.stringify(rec.params, null, 2)}</pre>}
    </div>
  )
}
