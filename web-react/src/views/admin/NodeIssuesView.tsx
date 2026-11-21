import { useEffect, useMemo, useRef, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { acknowledgeNodeIssue, type NodeIssueListParams } from '@/api/nodeIssues'
import type { NodeAgentIssue } from '@/api/types'
import { AsyncButton } from '@/components/common/AsyncButton'
import { PageHeader } from '@/components/common/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { DataTable } from '@/components/data/DataTable'
import { DataTablePagination } from '@/components/data/DataTablePagination'
import { DetailSheet } from '@/components/data/DetailSheet'
import { FilterBar } from '@/components/data/FilterBar'
import type { BulkContext } from '@/components/data/dataTableTypes'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTableViewState } from '@/hooks/useTableViewState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { IconCheck, IconRefresh } from '@/lib/icons'
import { nodeIssuesQuery } from '@/query/nodeIssues'
import { useQueryScope } from '@/query/useQueryScope'
import { groupNodeIssues, type NodeIssueGroup } from '@/utils/nodeIssueGroups'
import { allSettledLimited } from '@/utils/promises'
import { useCan } from '@/utils/permissions'
import { FIT, RecordCard, ResultCount, TimeCell, legacyPageSize, plainColumns, useAdminT } from './logs/shared'
import { ProblemCell, ReviewBadge, ServerCell } from './nodeIssues/cells'
import { GroupDetail } from './nodeIssues/GroupDetail'
import { REVIEW_STATES, VIEWS, acknowledgedParam, groupParam, issueTitle, pendingIds, serverNameOf, type IssueView, type ReviewState } from './nodeIssues/model'

/**
 * What the node agents reported, grouped by server and kind of problem so a hundred repeats of one
 * condition read as one line. Grouping is only presentation: every report keeps its own id, is listed
 * in the sheet, and is reviewed (acknowledged) by that id. "Reviewed" is a note that someone looked,
 * not a claim the node recovered, and these are reports, not a live health check; the page says both.
 */
export default function NodeIssuesView() {
  const t = useAdminT()
  const canOperate = useCan('sync.operate')
  const scope = useQueryScope()
  const [url, setUrl] = useUrlStates({
    view: urlCodecs.oneOf(VIEWS, 'attention'),
    state: urlCodecs.oneOf(REVIEW_STATES, 'unacknowledged'),
    q: urlCodecs.string(),
    page: urlCodecs.int(1, { min: 1 }),
  })
  const sel = useUrlSelection<string>('row', groupParam)
  const table = useTableViewState('admin.nodeIssues', { pageSize: legacyPageSize() })
  const pageSize = table.state.pageSize
  const keyword = url.q.trim()

  const params = useMemo<NodeIssueListParams>(() => {
    const p: NodeIssueListParams = { page: url.page, page_size: pageSize, view: url.view }
    const ack = acknowledgedParam(url.state)
    if (ack !== undefined) p.acknowledged = ack
    if (keyword) p.keyword = keyword
    return p
  }, [url.page, pageSize, url.view, url.state, keyword])
  // keepPreviousData: the last answer stays on screen while the next one loads or fails, and a
  // newer filter cancels an older request, so a slow answer cannot overwrite a newer one.
  const query = useQuery({ ...nodeIssuesQuery(scope, params), placeholderData: keepPreviousData })
  const items = query.data?.items
  const total = query.data?.total
  const groups = useMemo(() => groupNodeIssues(items ?? []), [items])
  const openGroup = sel.selected === null ? undefined : groups.find(g => g.id === sel.selected)

  // ---- selection: by group row, acting on the group's unreviewed record ids ----
  const [selection, setSelection] = useState<Record<string, boolean>>({})
  useEffect(() => { setSelection({}) }, [url.page, pageSize, url.view, url.state, keyword])
  useEffect(() => { if (!canOperate) setSelection({}) }, [canOperate])

  // ---- actions: one at a time. A second review while one runs would read stale rows. ----
  const lock = useRef(false)
  const latestCanOperate = useRef(canOperate)
  latestCanOperate.current = canOperate
  const [busyId, setBusyId] = useState<number | null>(null)
  const [batch, setBatch] = useState<{ state: '' | 'confirming' | 'running'; done: number; total: number }>({ state: '', done: 0, total: 0 })
  const locked = busyId !== null || batch.state !== ''

  async function acknowledgeOne(issue: NodeAgentIssue) {
    if (!canOperate || issue.acknowledged_at || lock.current) return
    lock.current = true
    setBusyId(issue.id)
    try {
      await acknowledgeNodeIssue(issue.id)
      pushSnack(t('node_issues.acknowledged'), 'success')
      await query.refetch()
    } catch { /* the request layer toasts the failure; never mark reviewed on failure */ } finally {
      lock.current = false
      setBusyId(null)
    }
  }

  async function acknowledgeMany(rawIds: number[]) {
    if (!canOperate || rawIds.length === 0 || lock.current) return
    // The exact ids, fixed before the question is asked: never widened to another page, a fresh
    // filter result or records that arrived meanwhile.
    const ids = [...new Set(rawIds)]
    lock.current = true
    setBatch({ state: 'confirming', done: 0, total: ids.length })
    try {
      const agreed = await confirm({
        title: t('node_issues.bulk.confirm_title'),
        message: t('node_issues.bulk.confirm_message', { records: t('node_issues.record_count', { count: ids.length }) }),
        confirmText: t('node_issues.bulk.acknowledge'),
      })
      if (!agreed || !latestCanOperate.current) return
      setBatch({ state: 'running', done: 0, total: ids.length })
      const results = await allSettledLimited(ids, async id => {
        try { await acknowledgeNodeIssue(id, { quiet: true }) } finally { setBatch(b => ({ ...b, done: b.done + 1 })) }
      })
      const failed = new Set(ids.filter((_, i) => results[i].status === 'rejected'))
      pushSnack(t(failed.size ? 'node_issues.bulk.partial' : 'node_issues.bulk.done', {
        ok: ids.length - failed.size, fail: failed.size, records: t('node_issues.record_count', { count: ids.length }),
      }), failed.size ? 'warning' : 'success')
      const fresh = await query.refetch()
      // Only the failures that still show as unreviewed stay selected: a success must not leave a
      // ticked row behind, and no review time is made up locally.
      setSelection(Object.fromEntries(groupNodeIssues(fresh.data?.items ?? [])
        .filter(g => pendingIds(g).some(id => failed.has(id))).map(g => [g.id, true])))
    } finally {
      lock.current = false
      setBatch({ state: '', done: 0, total: 0 })
    }
  }

  // A page that no longer exists (its last report was just reviewed) steps back to the last one that does.
  useEffect(() => {
    if (total === undefined || query.isFetching) return
    const last = Math.max(1, Math.ceil(total / pageSize))
    if (url.page > last) setUrl({ page: last })
  }, [total, pageSize, url.page, query.isFetching, setUrl])

  // A link to a group that is not on this page (reviewed since, another filter) opens nothing; drop it.
  const { selected, clear } = sel
  useEffect(() => {
    if (!query.isFetching && items !== undefined && selected !== null && !openGroup) clear()
  }, [query.isFetching, items, selected, openGroup, clear])

  const showState = url.state !== 'unacknowledged'
  const columns = useMemo<ColumnDef<NodeIssueGroup, any>[]>(() => plainColumns<NodeIssueGroup>([
    { id: 'problem', header: t('node_issues.table.problem'), meta: { label: t('node_issues.table.problem'), hideable: false, className: 'w-[40%] whitespace-normal' },
      cell: c => <ProblemCell group={c.row.original} view={url.view} showState={showState} /> },
    { id: 'server', header: t('node_issues.table.server'), meta: { label: t('node_issues.table.server'), className: 'w-[24%] whitespace-normal' },
      cell: c => <ServerCell group={c.row.original} /> },
    { id: 'records', header: t('node_issues.table.records'), meta: { label: t('node_issues.table.records'), align: 'right', className: 'w-24 tabular-nums' },
      cell: c => c.row.original.issues.length },
    { id: 'last_seen', header: t('node_issues.table.last_seen'), meta: { label: t('node_issues.table.last_seen'), className: `${FIT} w-44` },
      cell: c => <TimeCell value={c.row.original.lastSeenAt} /> },
  ]), [t, url.view, showState])

  const records = items?.length ?? 0
  const filtered = keyword !== '' || url.state !== 'unacknowledged'
  const empty = filtered && keyword !== ''
    ? <EmptyState compact title={t('node_issues.empty')} action={<Button variant="outline" size="sm" onClick={() => setUrl({ q: '', page: 1 })}>{t('logs.clear_filters')}</Button>} />
    : <EmptyState compact title={t(url.state === 'unacknowledged' && !keyword ? (url.view === 'attention' ? 'node_issues.empty_attention' : 'node_issues.empty_review') : 'node_issues.empty')} />

  const frame = (body: React.ReactNode) => (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('node_issues.title')}
        count={total}
        description={t('node_issues.subtitle')}
        secondary={<AsyncButton variant="outline" icon={<IconRefresh />} pending={query.isFetching} disabled={locked} onClick={() => query.refetch()}>{t('node_issues.refresh')}</AsyncButton>}
      />
      {body}
    </div>
  )

  if (query.isError && !query.data) {
    return frame(<ErrorState title={t('node_issues.load_failed_title')} reason={query.error} retry={() => query.refetch()} />)
  }

  const shown = openGroup
  return frame(
    <>
      <Tabs value={url.view} onValueChange={v => { if (!lock.current) setUrl({ view: v as IssueView, page: 1 }) }}>
        <TabsList aria-label={t('node_issues.view.label')}>
          {VIEWS.map(v => <TabsTrigger key={v} value={v} disabled={locked}>{t(`node_issues.view.${v}`)}</TabsTrigger>)}
        </TabsList>
      </Tabs>
      <p className="mt-3 text-xs text-muted-foreground">{t('node_issues.review_notice')}</p>
      {query.isError && (
        <Alert variant="destructive" className="mt-3">
          <AlertDescription>{t('node_issues.load_failed')}</AlertDescription>
        </Alert>
      )}
      <div className="mt-3">
        <DataTable<NodeIssueGroup>
          aria-label={t('node_issues.title')}
          mode="server"
          data={groups}
          columns={columns}
          getRowId={g => g.id}
          loading={query.isPending}
          // Paged by records, not by groups: the footer below counts what the server counts.
          pagination={false}
          maxHeight={null}
          selectable={canOperate}
          selection={selection}
          onSelectionChange={setSelection}
          rowLabel={g => `${issueTitle(g, url.view, t)} · ${serverNameOf(g, t)}`}
          rowOptions={g => (pendingIds(g).length === 0 ? { selectable: false } : undefined)}
          bulkActions={(ctx: BulkContext<NodeIssueGroup>) => (
            <>
              {batch.state === 'running' && <span role="status" className="text-sm text-muted-foreground tabular-nums">{t('node_issues.bulk.progress', { done: batch.done, total: batch.total })}</span>}
              <AsyncButton size="sm" icon={<IconCheck />} disabled={locked} pending={batch.state === 'running'} onClick={() => acknowledgeMany(ctx.loadedRows.flatMap(pendingIds))}>
                {t('node_issues.bulk.acknowledge')}
              </AsyncButton>
            </>
          )}
          onRowOpen={g => sel.select(g.id)}
          activeRowId={sel.selected}
          renderCard={g => (
            <RecordCard
              title={issueTitle(g, url.view, t)}
              status={showState ? <ReviewBadge group={g} /> : undefined}
              lines={[serverNameOf(g, t), <span key="m">{t('node_issues.record_count', { count: g.issues.length })} · <TimeCell value={g.lastSeenAt} /></span>]}
            />
          )}
          toolbar={(
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
              <FilterBar
                aria-label={t('node_issues.title')}
                search={{ value: url.q, onChange: q => { if (!lock.current) setUrl({ q, page: 1 }) }, placeholder: t('node_issues.search') }}
                onClearAll={() => setUrl({ q: '', page: 1 })}
              >
                <SegmentedControl<ReviewState>
                  aria-label={t('node_issues.table.state')}
                  value={url.state}
                  disabled={locked}
                  onChange={state => setUrl({ state, page: 1 })}
                  options={REVIEW_STATES.map(s => ({ value: s, label: t(`node_issues.filter.${s}`) }))}
                />
              </FilterBar>
              <ResultCount total={total} />
            </div>
          )}
          empty={empty}
        />
        {total !== undefined && total > 0 && (
          <DataTablePagination
            className="mt-3 rounded-xl border bg-card"
            page={url.page}
            pageSize={pageSize}
            total={total}
            shown={records}
            disabled={locked}
            onPageChange={page => { if (!lock.current) setUrl({ page }) }}
            onPageSizeChange={n => { if (!lock.current) { table.setPageSize(n); setUrl({ page: 1 }) } }}
          />
        )}
      </div>
      <DetailSheet
        open={shown !== undefined}
        onClose={sel.clear}
        title={shown ? issueTitle(shown, url.view, t) : ''}
        description={shown ? serverNameOf(shown, t) : undefined}
        status={shown ? <ReviewBadge group={shown} /> : undefined}
        // One report has its own button in the list; "all" only helps when there is more than one to review.
        actions={shown && canOperate && pendingIds(shown).length > 1 && (
          <AsyncButton size="sm" icon={<IconCheck />} disabled={locked} pending={batch.state === 'running'} onClick={() => acknowledgeMany(pendingIds(shown))}>
            {t('node_issues.acknowledge_all', { count: pendingIds(shown).length })}
          </AsyncButton>
        )}
      >
        {shown && <GroupDetail group={shown} view={url.view} canOperate={canOperate} busyId={busyId} locked={locked} onAcknowledge={acknowledgeOne} />}
      </DetailSheet>
    </>,
  )
}
