import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestHitItem, type DestHits } from '@/api/accessControl'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { DataTablePagination } from '@/components/data/DataTablePagination'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { LoadingState } from '@/components/feedback/LoadingState'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { IconWarning } from '@/lib/icons'
import { useAllNodes, useAllServers } from '@/query/nodes'
import { useDestHits, useDestPolicies, useDestSettings, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { collectingNodeCount, enabledPolicies, isNativeKind } from '@/utils/accessControl'
import AddExemptionDialog from '../sheets/AddExemptionDialog'
import type { SettingsFocus } from '../accessParams'
import { destErrorText } from '../destErrors'
import AddExceptionDialog, { type ExceptionTarget } from './AddExceptionDialog'
import { R } from './cells'
import { GroupList } from './GroupList'
import { HitList } from './HitList'
import { RecordsFilters } from './RecordsFilters'
import {
  parseRecordsParams, recordsApiParams, recordsFilterCount, recordsSearch, REC_PAGE_SIZES, type RecGroupBy, type RecordsFilter,
} from './recordsParams'

const SEARCH_DEBOUNCE_MS = 400

export interface RecordsTabProps {
  /** Opens the account drawer (the page's `user` param). */
  onOpenUser: (userId: number) => void
  /** Opens the test sheet with this destination (carried in history state, never the URL). */
  onTestDestination: (host: string) => void
  onOpenSettings: (focus?: SettingsFocus) => void
  onOpenNodes: () => void
  onGoToPolicies: () => void
}

/**
 * THE RECORDS TAB (S12): the hits, one row per account, destination and hour,
 * answering who, on which node, because of which rule, to what, and how many.
 * Filters, the views and the page are in the URL (`rec_*`, recordsParams);
 * the search term is not: it is component state and a request parameter only
 * (R12). The tiles count the whole time/account/node selection whatever the
 * action filter says, so pressing one never zeroes the other.
 *
 * Records name accounts and the destinations they reached; the tab is read-only except for the row
 * menu's exception, whose dialogs carry their own role checks on the server.
 */
export default function RecordsTab({ onOpenUser, onTestDestination, onOpenSettings, onOpenNodes, onGoToPolicies }: RecordsTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  // The details table folds into cards below 900px, as before; the filter row folds below 600px.
  const folded = useMediaQuery('(max-width: 899.95px)')
  const phone = useMediaQuery('(max-width: 599.95px)')
  const scope = useQueryScope()
  const tz = useSiteStore(s => s.timezone)
  const [params, setParams] = useSearchParams()
  const settings = useDestSettings(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const retention = settings.data?.effective.dest_hit_retention_days

  // The search term: what the admin typed, and what was last sent (after a pause).
  const [typed, setTyped] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const id = setTimeout(() => setQ(typed.trim()), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [typed])

  // One request per filter set: "now" is read when the filters change, never on a render, or the key would move every time.
  const recKey = [...params].filter(([k]) => k.startsWith('rec_')).map(([k, v]) => `${k}=${v}`).join('&')
  // The hour ticks the request forward: an open page keeps seeing new hour buckets.
  const [hourTick, setHourTick] = useState(() => Math.floor(Date.now() / 3_600_000))
  useEffect(() => {
    const id = setInterval(() => setHourTick(Math.floor(Date.now() / 3_600_000)), 60_000)
    return () => clearInterval(id)
  }, [])
  const { filter, request } = useMemo(() => {
    const f = parseRecordsParams(new URLSearchParams(recKey))
    return { filter: f, request: recordsApiParams(f, { now: Date.now(), retentionDays: retention, q }) }
    // hourTick moves the request's "now" forward; it is not read inside.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recKey, retention, q, hourTick])
  const hits = useDestHits(scope, request)

  const patch = (next: Partial<RecordsFilter>) => setParams(prev => recordsSearch(prev, next), { replace: true })
  const clear = () => {
    setTyped(''); setQ('')
    setParams(prev => recordsSearch(prev, { user_id: undefined, panel_id: undefined, exit_panel_id: undefined, source: undefined, action: undefined }), { replace: true })
  }

  const [exception, setException] = useState<ExceptionTarget | null>(null)
  const [exempting, setExempting] = useState<number | null>(null)

  const num = new Intl.NumberFormat(i18n.language)
  const data: DestHits | undefined = hits.data
  // One instant per read: the day headings and "today" are measured from it, not from each render.
  const readAt = hits.dataUpdatedAt
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const now = useMemo(() => Date.now(), [readAt])
  const filterCount = recordsFilterCount(filter) + (q ? 1 : 0)

  // ---- labels ----
  const ruleName = (h: Pick<DestHitItem, 'source' | 'source_name' | 'source_kind'>) => {
    if (h.source_name === null) return null
    if (h.source_kind === 'group') return t(`${R}rule.group_rule`, { name: h.source_name })
    return h.source_kind === 'source' ? t(`${R}rule.source_rule`, { name: h.source_name }) : h.source_name
  }
  const deletedRule = (source: string) => t(`${R}rule.${source.startsWith('s') ? 'deleted_source_rule' : source.startsWith('g') ? 'deleted_group' : 'deleted_policy'}`)
  const userName = (h: Pick<DestHitItem, 'user_id' | 'upn'>) => h.upn ?? t(`${R}rule.deleted_user`, { id: h.user_id })
  /** Where a hit left from: the exit server's name, or "this server" when it went out directly. */
  const exitLabel = (h: Pick<DestHitItem, 'exit_panel_id' | 'exit_panel_name'>) =>
    h.exit_panel_id === undefined ? '—' : h.exit_panel_id === 0 ? t(`${R}exit.direct`) : h.exit_panel_name ?? `#${h.exit_panel_id}`

  // ---- the rule filter's options: policies, source rules and the deleted, from what is there and what the range saw ----
  const ruleOptions = useMemo(() => {
    const byId = new Map<string, string>()
    for (const p of [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])]) {
      if (p.action === 'block') byId.set(`p${p.id}`, p.name)
    }
    const rules: { source: string; label: string }[] = []
    const allowlists: { source: string; label: string }[] = []
    const deleted: { source: string }[] = []
    for (const s of data?.sources ?? []) {
      if (s.name === null) deleted.push({ source: s.source })
      else if (s.source.startsWith('g')) allowlists.push({ source: s.source, label: t(`${R}rule.group_rule`, { name: s.name }) })
      else if (s.source.startsWith('s')) rules.push({ source: s.source, label: t(`${R}rule.source_rule`, { name: s.name }) })
      else byId.set(s.source, s.name)
    }
    return { policies: [...byId].map(([source, label]) => ({ source, label })), rules, allowlists, deleted }
  }, [policies.data, data?.sources, t])
  // Exit servers exist only where a tunnel does: a node that leaves through another server. Without one, nothing of this shows.
  const nodeList = useAllNodes(scope)
  const exitIds = useMemo(() => [...new Set((nodeList.data ?? []).map(n => n.egress_panel_id ?? 0).filter(id => id > 0))].sort((a, b) => a - b), [nodeList.data])
  const hasTunnels = exitIds.length > 0
  // A link may carry an exit filter or view: it stays visible for as long as it is in force.
  const showExit = hasTunnels || filter.exit_panel_id !== undefined || filter.group_by === 'exit'
  const serverList = useAllServers(scope, { enabled: showExit })
  const serverNames = useMemo(() => new Map((serverList.data ?? []).map(s => [s.id, s.name])), [serverList.data])
  // The exit servers the tunnels name, whether or not they record hits; a linked exit that no tunnel names any more stays while it is the filter.
  const exitOptions = filter.exit_panel_id && !exitIds.includes(filter.exit_panel_id) ? [...exitIds, filter.exit_panel_id] : exitIds
  const panels = (status.data?.nodes ?? []).filter(n => isNativeKind(n.kind) && n.state !== 'unsupported_kind')
  const exitName = (id: number) => serverNames.get(id) ?? panels.find(n => n.panel_id === id)?.panel_name ?? `#${id}`

  // ---- state of the page ----
  const deployed = enabledPolicies(policies.data).length
  const recording = collectingNodeCount(status.data)
  const rangeDays = /^(\d+)d$/.exec(filter.since)?.[1]
  const olderThanRetention = !!retention && !!rangeDays && Number(rangeDays) > retention

  const views: RecGroupBy[] = showExit ? ['none', 'site', 'user', 'policy', 'exit'] : ['none', 'site', 'user', 'policy']

  /** The row menu: test the destination, add an exception for it, open the account. */
  const menuOf = (h: DestHitItem): MoreMenuItem[] => [
    { id: 'test', label: t(`${R}menu.test`), onSelect: () => onTestDestination(h.dest) },
    {
      id: 'exception', label: t(`${R}menu.exception`),
      // A hit refused by an allowlist group defaults to that group's supplement list; a group that is gone defaults to nothing.
      onSelect: () => setException({
        host: h.dest,
        user: h.upn ? { id: h.user_id, upn: h.upn } : null,
        group: h.source_kind === 'group' && h.source_name ? { id: Number(h.source.slice(1)), name: h.source_name } : null,
        groupGone: h.source_kind === 'group' && !h.source_name,
      }),
    },
    { id: 'open_user', label: t(`${R}menu.open_user`), disabled: !h.upn, onSelect: () => onOpenUser(h.user_id) },
  ]

  // ---- the list ----
  const grouped = filter.group_by !== 'none'
  const refusalCode = hits.isError ? destErrorOf(hits.error)?.code : undefined
  const refusal = refusalCode === 'dest_hits_range' || refusalCode === 'dest_hits_invalid'
  let body: ReactNode
  if (hits.isPending) {
    body = <LoadingState />
  } else if (hits.isError && (!data || refusal)) {
    // A refused range or filter is the admin's to fix, and the page kept from the last read would hide it: say it either way.
    body = isDestUnavailable(hits.error)
      ? <Alert>{t('admin:access_control.unwired')}</Alert>
      : refusal
        ? <Alert variant="destructive">{destErrorText(t, hits.error)}</Alert>
        : <ErrorState compact title={t(`${R}read_failed`)} reason={hits.error} retry={() => hits.refetch()} />
  } else if (data && data.items.length === 0) {
    const empty = (title: string, action?: ReactNode) => <EmptyState compact title={title} action={action} />
    if (deployed === 0 && filterCount === 0) body = empty(t(`${R}empty.no_policies`), <Button size="sm" variant="outline" onClick={onGoToPolicies}>{t(`${R}empty.to_policies`)}</Button>)
    else if (recording === 0 && status.data) body = empty(t(`${R}empty.no_collect`), <Button size="sm" variant="outline" onClick={onOpenNodes}>{t(`${R}empty.nodes`)}</Button>)
    else if (filterCount > 0) body = empty(t(`${R}empty.filtered`), <Button size="sm" variant="outline" onClick={clear}>{t(`${R}empty.clear`)}</Button>)
    else if (olderThanRetention) body = empty(t(`${R}empty.older`, { days: retention }))
    else body = empty(t(`${R}empty.none`))
  } else if (data) {
    body = grouped
      ? <GroupList data={data} view={filter.group_by} folded={folded} num={num} userName={userName} ruleName={ruleName} exitLabel={exitLabel}
        onOpenUser={onOpenUser} onPickSource={s => patch({ source: s })} />
      : <HitList data={data} folded={folded} tz={tz} now={now} num={num} userName={userName} ruleName={ruleName} exitLabel={exitLabel} showExit={showExit}
        onOpenUser={onOpenUser} onPickSource={s => patch({ source: s })} menuOf={menuOf} />
  }

  const hideTiles = !data || (data.items.length === 0 && (deployed === 0 || (recording === 0 && !!status.data)))
  return (
    <div>
      {!hits.isPending && data && !hideTiles && (
        <div data-testid="hits-tiles">
          <KpiGrid className="mt-0">
            {(['block', 'observe'] as const).map(a => (
              <KpiTile key={a} label={t(`${R}tiles.${a}`)} value={num.format(a === 'block' ? data.summary.block : data.summary.observe)}
                pressed={filter.action === a} onToggle={() => patch({ action: filter.action === a ? undefined : a })} />
            ))}
            <KpiTile label={t(`${R}tiles.users`)} value={num.format(data.summary.users)} />
          </KpiGrid>
        </div>
      )}

      <RecordsFilters filter={filter} patch={patch} typed={typed} onTyped={setTyped} retention={retention} ruleOptions={ruleOptions}
        deletedRule={deletedRule} panels={panels} showExit={showExit} exitOptions={exitOptions} exitName={exitName} filterCount={filterCount} phone={phone} />

      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-muted-foreground">{t(`${R}view.label`)}</span>
          <SegmentedControl<RecGroupBy> aria-label={t(`${R}view.label`)} value={filter.group_by} onChange={v => patch({ group_by: v })}
            options={views.map(v => ({ value: v, label: t(`${R}view.${v}`) }))} />
        </div>
        {data && data.items.length > 0 && (
          <span className="text-sm text-muted-foreground">
            {grouped ? t(`${R}count.grouped`, { n: num.format(data.total) }) : t(`${R}count.detail`, { n: num.format(data.total) })}
          </span>
        )}
      </div>
      {data && data.dropped_in_range > 0 && (
        <p data-testid="hits-dropped" className="mb-2 flex items-center gap-1.5 text-sm">
          <IconWarning aria-hidden className="size-4 text-warning-ink" />{t(`${R}dropped`, { n: num.format(data.dropped_in_range) })}
        </p>
      )}

      {/* A fixed slot, so a re-read does not move the table. */}
      <div aria-hidden className="mb-1 h-1 overflow-hidden rounded-full">{hits.isFetching && !hits.isPending && <div className="h-full w-1/3 animate-pulse rounded-full bg-primary" />}</div>
      {body}

      {data && data.items.length > 0 && (
        <DataTablePagination total={data.total} page={filter.page} pageSize={filter.page_size} shown={data.items.length} pageSizeOptions={REC_PAGE_SIZES}
          onPageChange={n => patch({ page: n })} onPageSizeChange={n => patch({ page_size: n })} />
      )}
      <p className="mt-2 text-sm text-muted-foreground">
        {retention ? t(`${R}retention`, { days: retention }) : ''}{' '}
        <Button type="button" variant="link" className="h-auto p-0 align-baseline" onClick={() => onOpenSettings('retention')}>{t(`${R}change`)}</Button>
      </p>

      <AddExceptionDialog target={exception} onClose={() => setException(null)} onExempt={id => setExempting(id)} />
      <AddExemptionDialog open={exempting !== null} onClose={() => setExempting(null)} lockedUserId={exempting ?? undefined} />
    </div>
  )
}
