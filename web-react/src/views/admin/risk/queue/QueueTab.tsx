import { useMemo, useRef, type ReactNode, type RefObject } from 'react'
import { Link, useSearchParams } from 'react-router'
import { isAxiosError } from 'axios'
import type { ColumnDef } from '@tanstack/react-table'

import {
  QUEUE_SOURCE_FILTERS, type QueueParams, type QueueRow, type QueueStatus, type QueueView,
} from '@/api/riskCenter'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import type { Tone } from '@/components/common/tones'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertAction, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useRiskQueue } from '@/query/riskCenter'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import { activeDbIsCountryOnly } from '@/utils/geoAnomaly'
import { regionNamer } from '@/utils/regionName'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { listSeparator } from '@/utils/riskCenter'
import { FilterChip, plainColumns } from '../../logs/shared'
import { availableActions, DESTRUCTIVE_ACTIONS, subjectOfRow, type RiskActionKind } from '../actions/riskSubject'
import { useRiskActions } from '../actions/useRiskActions'
import { ReviewBadges } from '../drawer/DrawerHeader'
import { DetectorStateChip } from '../evidence/DetectorStateChip'
import { parseQueueParams, QUEUE_PAGE_SIZES, queueSearch } from '../riskParams'
import { useRiskT } from '../useRiskT'
import QueueMetricCards, { type MetricCard } from './QueueMetricCards'
import { headlineText, headlineTip, sourceChipLabel } from './queueText'

/** What each metric card sets; every card first clears what the others set,
 *  so one card is in force at a time. */
const CARD_FILTERS: Record<MetricCard, Partial<QueueParams>> = {
  flagged: { level: 'flagged' },
  suspect: { level: 'suspect' },
  // Every held account, whatever its review state (D17).
  auto: { auto_suspended: true, status: 'all' },
  dismissed: { status: 'dismissed' },
}
// The bell's `urgent` goes too, as it does on any status choice: it is where
// the admin lands, not a narrowing under every later choice. Urgent is open
// by definition, so kept under 已忽略 it would list nobody while the card
// counts its accounts. The chip is the way to drop it without a new choice.
const NO_CARD: Partial<QueueParams> = { level: undefined, auto_suspended: false, status: 'open', urgent: false }

/** The card whose filter is the one in force, or null. */
function activeCard(p: QueueParams): MetricCard | null {
  if (p.level && p.status === 'open' && !p.auto_suspended) return p.level
  if (p.auto_suspended && p.status === 'all' && !p.level) return 'auto'
  if (p.status === 'dismissed' && !p.level && !p.auto_suspended) return 'dismissed'
  return null
}

const STATUSES: readonly QueueStatus[] = ['open', 'dismissed', 'trusted', 'all']

/** A signal's tone by its level: the flag is the alarm, suspect the ramp. */
function levelTone(level: string): Tone {
  return level === 'flagged' ? 'danger' : level === 'suspect' ? 'warning' : 'neutral'
}

/** What the failed read says: the server's words, else the transport's. */
function errorText(err: unknown): string {
  if (isAxiosError(err)) return String((err.response?.data as { error?: string } | undefined)?.error ?? err.message)
  return String(err)
}

/** What a row's columns and its menu call, read through a ref so a re-render does not rebuild every row. */
interface RowAct {
  start: (kind: RiskActionKind, row: QueueRow) => void
}

/**
 * 待处理: ONE ROW PER ACCOUNT that needs a look, most urgent first (the
 * server sorts: flagged and held, flagged, held, suspect, then the latest
 * change). A row says what is wrong in one line and opens the account's
 * drawer; its ⋯ menu offers the same actions as the drawer, decided from the
 * row alone.
 *
 * Every filter lives in the URL (riskParams): the metric cards, the status,
 * the sources, the search, the bell's "needs action now" and the page. So
 * opening an account and coming Back, a reload, the bell or a copied link all
 * show the same list. Filters are written by REPLACE and each resets the
 * page.
 */
export default function QueueTab({ onOpenUser, onOpenLive, onOpenPolicy }: {
  onOpenUser: (userId: number) => void
  onOpenLive: () => void
  /** The policy tab, where the detectors' switches are. */
  onOpenPolicy: () => void
}) {
  const { t, i18n } = useRiskT()
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const [urlParams, setUrlParams] = useSearchParams()
  const params = parseQueueParams(urlParams)
  const q = useRiskQueue(scope, params)
  // Advisory: a failed read shows no notice. No evidence of a dead database
  // is not evidence of one.
  const { data: geoip } = useGeoIPStatus(scope)
  const actions = useRiskActions()

  // The patch is applied to the URL as it is when the write happens (the functional form), so a card
  // clicked while the search box waits out its pause is kept rather than undone by a stale copy.
  const update = (patch: Partial<QueueParams>) => setUrlParams(prev => queueSearch(prev, patch), { replace: true })

  const card = activeCard(params)
  const toggleCard = (c: MetricCard) => update(card === c ? NO_CARD : { ...NO_CARD, ...CARD_FILTERS[c] })

  const data: QueueView | undefined = q.data
  const dbDown = geoip !== undefined && (!geoip.enabled || !geoip.active)
  const status503 = isAxiosError(q.error) && q.error.response?.status === 503
  const narrowed = !!(params.source || params.level || params.auto_suspended || params.urgent || params.q)
    || (params.page ?? 1) > 1
  const clearAll = () => update({ q: '', source: '', level: undefined, auto_suspended: false, urgent: false, status: 'open' })

  // The columns are memoised on what words they read; the handlers they call come through a ref.
  const act = useRef<RowAct>({ start: () => {} })
  act.current = { start: (kind, row) => actions.start(kind, subjectOfRow(row)) }
  const language = i18n.language
  const columns = useMemo<ColumnDef<QueueRow, any>[]>(() => {
    const ctx = { nameRegion: regionNamer(t, language), panelTz, sep: listSeparator(language) }
    return plainColumns<QueueRow>([
      { id: 'user', header: t('admin:risk_center.queue.col_user'),
        meta: { label: t('admin:risk_center.queue.col_user'), hideable: false, className: 'w-[18%] max-w-0' },
        cell: c => <UserCell row={c.row.original} /> },
      { id: 'level', header: t('admin:risk_center.queue.col_level'),
        meta: { label: t('admin:risk_center.queue.col_level'), className: 'w-36' },
        cell: c => <LevelCell row={c.row.original} /> },
      { id: 'sources', header: t('admin:risk_center.queue.col_sources'),
        meta: { label: t('admin:risk_center.queue.col_sources'), className: 'w-[16%]' },
        cell: c => <SignalChips row={c.row.original} /> },
      { id: 'reason', header: t('admin:risk_center.queue.col_reason'),
        meta: { label: t('admin:risk_center.queue.col_reason'), className: 'w-[28%] max-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-xs text-muted-foreground' },
        cell: c => {
          const row = c.row.original
          const text = headlineText(row, t, ctx)
          return <span title={headlineTip(row, t) || text}>{text}</span>
        } },
      { id: 'changed', header: t('admin:risk_center.queue.col_changed'),
        meta: { label: t('admin:risk_center.queue.col_changed'), className: 'w-24 whitespace-nowrap' },
        cell: c => <ChangedCell row={c.row.original} /> },
      { id: 'review', header: t('admin:risk_center.queue.col_review'),
        meta: { label: t('admin:risk_center.queue.col_review'), className: 'w-32' },
        cell: c => <div className="flex flex-wrap gap-1"><ReviewBadges review={c.row.original.review} /></div> },
      { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('admin:risk_center.queue.col_actions'), className: 'w-12' },
        cell: c => <RowMenu row={c.row.original} act={act} /> },
    ])
  }, [t, language, panelTz])

  const emptyText = narrowed
    ? t('admin:risk_center.queue.empty_filtered')
    : params.status === 'dismissed'
      ? t('admin:risk_center.queue.empty_dismissed')
      : params.status === 'trusted'
        ? t('admin:risk_center.queue.empty_trusted')
        : t('admin:risk_center.queue.empty')
  // Empty because nothing is judged, not because nobody is sharing: say so, and offer the switches.
  const nothingJudged = !narrowed && params.status === 'open' && data?.global_detectors_off === true
  const empty = narrowed
    ? <EmptyState compact title={emptyText} action={<Button variant="outline" size="sm" onClick={clearAll}>{t('admin:risk_center.queue.clear_filters')}</Button>} />
    : <EmptyState compact title={emptyText} description={nothingJudged ? t('admin:risk_center.queue.global_off') : undefined}
      action={nothingJudged ? <Button variant="outline" size="sm" onClick={onOpenPolicy}>{t('admin:risk_center.queue.open_policy')}</Button> : undefined} />

  const sources = params.source ? params.source.split(',') : []
  const toolbar = (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <SegmentedControl<QueueStatus> value={params.status ?? 'open'} aria-label={t('admin:risk_center.queue.filter_status')}
        options={STATUSES.map(s => ({ value: s, label: t(`admin:risk_center.queue.status_${s}`) }))}
        onChange={v => update({ status: v, urgent: false })} />
      <FilterBar aria-label={t('admin:risk_center.queue.title')}
        search={{ value: params.q ?? '', onChange: v => update({ q: v }), placeholder: t('admin:risk_center.queue.search') }}
        facets={[{
          id: 'source', label: t('admin:risk_center.queue.filter_source'),
          options: QUEUE_SOURCE_FILTERS.map(s => ({ value: s, label: t(`admin:risk_center.flags.source.${s}`) })),
          selected: sources, onChange: next => update({ source: next.join(',') }),
        }]}
        onClearAll={narrowed ? clearAll : undefined}>
        {params.urgent && (
          <FilterChip label={t('admin:risk_center.queue.filter_urgent')} removeLabel={t('admin:risk_center.queue.filter_urgent_remove')}
            onRemove={() => update({ urgent: false })} />
        )}
      </FilterBar>
    </div>
  )

  return (
    <div className="flex flex-col gap-3">
      <QueueMetricCards counts={data?.counts} active={card} onToggle={toggleCard} onOpenLive={onOpenLive} />

      {/* Without a location database three detectors read "cannot tell", and an empty queue would look
          like a clean fleet. */}
      {dbDown && (
        <Alert>
          <AlertDescription>{t('admin:risk_center.queue.geoip_unavailable')}</AlertDescription>
          <AlertAction>
            <Button variant="outline" size="sm" render={<Link to="/admin/settings?tab=general" />}>
              {t('admin:risk_center.queue.geoip_open')}
            </Button>
          </AlertAction>
        </Alert>
      )}
      {!dbDown && activeDbIsCountryOnly(geoip) && (
        <Alert><AlertDescription>{t('admin:geo_anomalies.coarse_db')}</AlertDescription></Alert>
      )}
      {(data?.counts.geo_unknown ?? 0) > 0 && (
        <p className="text-xs text-muted-foreground">
          {t('admin:risk_center.queue.geo_unknown', { count: data?.counts.geo_unknown })}
        </p>
      )}

      {status503 ? (
        <EmptyState compact title={t('admin:risk_center.unwired')} />
      ) : (
        <>
          {/* A new filter keeps the previous page on screen while it loads; the bar says the list is
              being read again. */}
          <div className="h-0.5 overflow-hidden rounded-full bg-muted" aria-hidden={!(q.isFetching && data)}>
            {q.isFetching && data && <div role="progressbar" aria-label={t('admin:risk_center.queue.reloading')} className="h-full w-1/3 animate-pulse rounded-full bg-primary" />}
          </div>
          <DataTable<QueueRow>
            aria-label={t('admin:risk_center.queue.title')}
            mode="server"
            data={data?.items ?? []}
            total={data?.total}
            columns={columns}
            getRowId={r => String(r.user_id)}
            loading={q.isPending}
            error={q.error ? t('admin:risk_center.load_failed', { error: errorText(q.error) }) : null}
            onRetry={() => void q.refetch()}
            page={params.page ?? 1}
            onPageChange={p => update({ page: p })}
            pageSize={params.page_size ?? 25}
            onPageSizeChange={n => update({ page_size: n })}
            pageSizeOptions={QUEUE_PAGE_SIZES}
            columnMenu={false}
            maxHeight={null}
            onRowOpen={r => onOpenUser(r.user_id)}
            renderCard={r => <QueueCard row={r} act={act} />}
            toolbar={toolbar}
            empty={empty}
          />
        </>
      )}
      {actions.dialogs}
    </div>
  )
}

/** The account's name, and under it its display name and group. Rows open on click or Enter. */
function UserCell({ row }: { row: QueueRow }) {
  const name = row.upn || row.display_name || `#${row.user_id}`
  const sub = [row.display_name, row.group_name].filter(Boolean).join(' · ')
  return (
    <div className="min-w-0">
      <div className="truncate font-medium" title={name}>{name}</div>
      {sub && <div className="truncate text-xs text-muted-foreground" title={sub}>{sub}</div>}
    </div>
  )
}

/** The level in words, and the detector's own hold by its one name, with since when in its title. */
function LevelCell({ row }: { row: QueueRow }) {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  return (
    <div className="flex flex-wrap items-center gap-1">
      {(row.level === 'flagged' || row.level === 'suspect') && <DetectorStateChip state={row.level} />}
      {row.auto_suspended && (
        <span title={t('admin:geo_anomalies.auto_suspended_since', { time: formatMsDualTz(row.service_disabled_at_ms, panelTz) })}>
          <StatusBadge tone="danger" label={t('admin:users.status.geo_auto')} />
        </span>
      )}
    </div>
  )
}

/** One quiet marker per signal, its level in the shape as well as the words. */
function SignalChips({ row }: { row: QueueRow }) {
  const { t } = useRiskT()
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1">
      {row.sources.filter(s => s.source !== 'geo_auto').map(s => (
        <StatusDot key={s.source} tone={levelTone(s.level)} label={sourceChipLabel(s.source, row, t)} className="text-xs" />
      ))}
    </div>
  )
}

/** Relative in the list (a dual-timezone string per row would double the column), exact in panel time in
 *  the title. "—" when nothing changed on record, never "just now". */
function ChangedCell({ row }: { row: QueueRow }): ReactNode {
  const { t } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  if (row.changed_at_ms <= 0) return <span className="text-muted-foreground">—</span>
  return <span title={formatMsDualTz(row.changed_at_ms, panelTz)}>{formatRelativeTimeShort(Date.now() - row.changed_at_ms, t)}</span>
}

/**
 * The row's one menu: the drawer's action matrix, decided from the row alone (no read). Rows open the
 * drawer on click, so the menu is the only control in a row; the actions that take access away sit last,
 * under a divider.
 */
function RowMenu({ row, act }: { row: QueueRow; act: RefObject<RowAct> }) {
  const { t } = useRiskT()
  const name = row.upn || row.display_name || `#${row.user_id}`
  const items: MoreMenuItem[] = availableActions(subjectOfRow(row), 'menu').map(k => ({
    id: k, label: t(`admin:risk_center.actions.${k}`), destructive: DESTRUCTIVE_ACTIONS.includes(k),
    onSelect: () => act.current.start(k, row),
  }))
  if (items.length === 0) return null
  return <MoreMenu size="icon-xs" label={t('admin:risk_center.queue.row_menu', { upn: name })} items={items} />
}

/** A row at phone width: the account and its level, the reason, and the time. The card opens the drawer. */
function QueueCard({ row, act }: { row: QueueRow; act: RefObject<RowAct> }) {
  const { t, i18n } = useRiskT()
  const panelTz = useSiteStore(s => s.timezone)
  const ctx = { nameRegion: regionNamer(t, i18n.language), panelTz, sep: listSeparator(i18n.language) }
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex min-w-0 items-start gap-2">
        <div className="min-w-0 flex-1"><UserCell row={row} /></div>
        <RowMenu row={row} act={act} />
      </div>
      <LevelCell row={row} />
      <p className="text-xs text-muted-foreground">{headlineText(row, t, ctx)}</p>
      <div className="flex flex-wrap items-center gap-2 text-xs"><SignalChips row={row} /><ChangedCell row={row} /><ReviewBadges review={row.review} /></div>
    </div>
  )
}
