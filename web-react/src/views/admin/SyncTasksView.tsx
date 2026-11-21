import { useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { cancelSyncTask, purgeFinishedSyncTasks, retrySyncTask, type SyncTaskListParams } from '@/api/syncTasks'
import type { SyncTask, SyncTaskStatus, SyncTaskType } from '@/api/types'
import { AsyncButton, AsyncIconButton } from '@/components/common/AsyncButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import type { BulkContext } from '@/components/data/dataTableTypes'
import { useUrlStates } from '@/hooks/useUrlState'
import { IconDelete, IconRefresh } from '@/lib/icons'
import { syncTasksQuery } from '@/query/syncTasks'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'
import { useCan } from '@/utils/permissions'
import { LogList } from './logs/LogList'
import { FIT, RecordCard, TimeCell, choiceCodec, plainColumns, singleFacet, useAdminT } from './logs/shared'
import { useLogView } from './logs/useLogView'
import { StatusCell } from './syncTasks/cells'
import { STATUSES, TYPES, attemptsOf, idOf, isRetired, lastErrorOf, nextRunOf, statusOf, summaryOf, targetOf, typeOf } from './syncTasks/model'
import { TaskDetail } from './syncTasks/TaskDetail'

/**
 * The queue of changes the panel could not push to an upstream server at once, and retries every
 * minute. The shell's "saved, but queued" toast links here (SYNC_TASKS_ROUTE), so the route stays.
 * Retry and cancel are open to everyone who can see the page; clearing the finished ones is admin only.
 */
export default function SyncTasksView() {
  const t = useAdminT()
  const canConfig = useCan('config.write')
  const scope = useQueryScope()
  const view = useLogView('admin.syncTasks')
  const [f, setF] = useUrlStates({ status: choiceCodec(STATUSES), type: choiceCodec(TYPES) })
  const params = useMemo<SyncTaskListParams>(() => ({
    page: view.page,
    page_size: view.pageSize,
    ...(f.status ? { status: f.status } : {}),
    ...(f.type ? { type: f.type } : {}),
  }), [view.page, view.pageSize, f.status, f.type])
  // The query options, not the `useSyncTasks` hook: the list keeps the last page on screen while the next one loads.
  const query = useQuery({ ...syncTasksQuery(scope, params), placeholderData: keepPreviousData })
  const items = query.data?.items
  const pendingCount = useMemo(() => (items ?? []).filter(r => statusOf(r) === 'pending').length, [items])

  // A batch is assembled on one page of one filter. Reset when that changes, NOT on every read: a plain
  // refresh used to drop a batch the operator was still putting together.
  const [selection, setSelection] = useState<Record<string, boolean>>({})
  useEffect(() => { setSelection({}) }, [view.page, view.pageSize, f.status, f.type])
  const [batchBusy, setBatchBusy] = useState<'' | 'retry' | 'cancel'>('')
  const [cancelling, setCancelling] = useState<ReadonlySet<number>>(new Set())
  const purging = useRef(false)

  const refetch = () => query.refetch()

  async function retry(r: SyncTask) {
    if (isRetired(r)) return
    await retrySyncTask(idOf(r))
    await refetch()
  }

  async function cancel(r: SyncTask) {
    const id = idOf(r)
    if (isRetired(r) || cancelling.has(id)) return
    const ty = typeOf(r)
    const ok = await confirm({
      title: t('sync_tasks.confirm.cancel_title'),
      message: t('sync_tasks.confirm.cancel_message', { id, type: ty ? t(`sync_tasks.type.${ty}`) : '' }),
      // Not just "Cancel": that is also the dialog's own way out.
      confirmText: t('sync_tasks.confirm.cancel_confirm'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setCancelling(prev => new Set(prev).add(id))
    try {
      await cancelSyncTask(id)
      await refetch()
    } catch { /* reported by the request layer */ } finally {
      setCancelling(prev => { const next = new Set(prev); next.delete(id); return next })
    }
  }

  async function runBatch(kind: 'retry' | 'cancel', rows: SyncTask[]) {
    if (!rows.length || batchBusy) return
    setBatchBusy(kind)
    try {
      const results = await allSettledLimited(rows, r => (kind === 'retry' ? retrySyncTask : cancelSyncTask)(idOf(r)))
      const failed = rows.filter((_, i) => results[i].status === 'rejected')
      if (failed.length > 0) pushSnack(t('sync_tasks.toast.batch_partial', { ok: rows.length - failed.length, fail: failed.length }), 'warning')
      else pushSnack(t(kind === 'retry' ? 'sync_tasks.toast.batch_retried' : 'sync_tasks.toast.batch_canceled', { count: rows.length }), 'success')
      // What worked leaves the batch; what failed stays selected so it can be tried again.
      setSelection(Object.fromEntries(failed.map(r => [String(idOf(r)), true])))
      await refetch()
    } finally { setBatchBusy('') }
  }

  async function batchCancel(rows: SyncTask[]) {
    if (!rows.length) return
    const ok = await confirm({
      title: t('sync_tasks.confirm.batch_cancel_title'),
      message: t('sync_tasks.confirm.batch_cancel_message', { count: rows.length }),
      confirmText: t('sync_tasks.batch_cancel'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (ok) await runBatch('cancel', rows)
  }

  async function purge() {
    if (purging.current) return
    purging.current = true
    try {
      const ok = await confirm({
        title: t('sync_tasks.confirm.purge_title'),
        message: t('sync_tasks.confirm.purge_message'),
        confirmText: t('sync_tasks.purge'),
        destructive: true,
        initialFocus: 'cancel',
      })
      if (!ok) return
      const res = await purgeFinishedSyncTasks()
      pushSnack(t('sync_tasks.toast.purged', { count: res.deleted }), 'success')
      await refetch()
    } catch { /* reported by the request layer */ } finally { purging.current = false }
  }

  // Columns are memoised on `t` only; the handlers they call are read through a ref, so a re-render
  // after a click does not rebuild every row.
  const act = useRef<Act>({ retry, cancel, open: (id: number) => view.sel.select(id) })
  act.current = { retry, cancel, open: (id: number) => view.sel.select(id) }

  const columns = useMemo<ColumnDef<SyncTask, any>[]>(() => plainColumns<SyncTask>([
    { id: 'status', header: t('sync_tasks.table.status'), meta: { label: t('sync_tasks.table.status'), hideable: false, className: 'w-44' },
      cell: c => <StatusCell task={c.row.original} /> },
    { id: 'type', header: t('sync_tasks.table.type'), meta: { label: t('sync_tasks.table.type'), className: `${FIT} w-[14%]` },
      cell: c => { const ty = typeOf(c.row.original); return ty ? t(`sync_tasks.type.${ty}`) : '—' } },
    { id: 'target', header: t('sync_tasks.table.target'), meta: { label: t('sync_tasks.table.target'), className: `${FIT} w-[10%] font-mono text-[0.8125rem]` },
      cell: c => <span title={targetOf(c.row.original)}>{targetOf(c.row.original)}</span> },
    { id: 'summary', header: t('sync_tasks.table.summary'), meta: { label: t('sync_tasks.table.summary'), className: `${FIT} w-[24%]` },
      cell: c => <span title={summaryOf(c.row.original)}>{summaryOf(c.row.original)}</span> },
    { id: 'attempts', header: t('sync_tasks.table.attempts'), meta: { label: t('sync_tasks.table.attempts'), align: 'right', className: 'w-20 tabular-nums' },
      cell: c => attemptsOf(c.row.original) },
    // The next try only means something while the task is waiting; after that the stored time is a leftover.
    { id: 'next_run', header: t('sync_tasks.table.next_run'), meta: { label: t('sync_tasks.table.next_run'), className: 'w-44' },
      cell: c => statusOf(c.row.original) === 'pending' ? <TimeCell value={nextRunOf(c.row.original)} /> : '—' },
    { id: 'error', header: t('sync_tasks.table.error'), meta: { label: t('sync_tasks.table.error'), className: `${FIT} w-[22%] text-xs text-destructive` },
      cell: c => <span title={lastErrorOf(c.row.original)}>{lastErrorOf(c.row.original)}</span> },
    { id: 'id', header: t('sync_tasks.table.id'), meta: { label: t('sync_tasks.table.id'), defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: c => String(idOf(c.row.original)) },
    { id: 'actions', header: '', meta: { hideable: false, align: 'right', label: t('sync_tasks.table.actions'), className: 'w-24' },
      cell: c => <RowActions task={c.row.original} act={act} /> },
  ]), [t])

  const status = singleFacet<SyncTaskStatus>(f.status, v => { setF({ status: v }); view.setUrl({ page: 1 }) })
  const type = singleFacet<SyncTaskType>(f.type, v => { setF({ type: v }); view.setUrl({ page: 1 }) })
  const words = { loadFailed: t('sync_tasks.load_failed'), refreshFailed: t('sync_tasks.refresh_failed'), filteredEmpty: t('sync_tasks.empty_filtered') }

  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('sync_tasks.title')}
        count={query.data?.total}
        description={t('sync_tasks.pending_count', { count: pendingCount })}
        // Not polled (see syncTasksQuery), so a manual refresh is the only way this list updates: on a slow
        // link it must visibly go busy or the click reads as having done nothing. isFetching, not isPending,
        // which is false again once the first page has loaded, even mid-refetch.
        secondary={<AsyncButton variant="outline" icon={<IconRefresh />} pending={query.isFetching} onClick={refetch}>{t('sync_tasks.refresh')}</AsyncButton>}
        more={canConfig ? [{ id: 'purge', label: t('sync_tasks.purge'), icon: IconDelete, destructive: true, onSelect: () => void purge() }] : []}
      />
      <LogList<SyncTask>
        label={t('sync_tasks.title')}
        view={view}
        query={query}
        columns={columns}
        getId={idOf}
        words={words}
        facets={[
          { id: 'status', label: t('sync_tasks.filter_status'), options: STATUSES.map(s => ({ value: s, label: t(`sync_tasks.status.${s}`) })), ...status },
          { id: 'type', label: t('sync_tasks.filter_type'), options: TYPES.map(ty => ({ value: ty, label: t(`sync_tasks.type.${ty}`) })), ...type },
        ]}
        narrowed={f.status !== '' || f.type !== ''}
        onClearAll={() => { setF({ status: '', type: '' }); view.setUrl({ page: 1 }) }}
        emptyTitle={t('sync_tasks.empty')}
        emptyDescription={t('sync_tasks.empty_hint')}
        selection={{
          value: selection,
          onChange: setSelection,
          rowLabel: r => `#${idOf(r)}`,
          // A retired task is read-only: no checkbox, and it is left out of select-all.
          rowOptions: r => (isRetired(r) ? { selectable: false, state: 'dimmed' } : undefined),
          bulkActions: (ctx: BulkContext<SyncTask>) => (
            <>
              <AsyncButton size="sm" variant="outline" icon={<IconRefresh />} disabled={batchBusy !== ''} pending={batchBusy === 'retry'} onClick={() => runBatch('retry', ctx.loadedRows.filter(r => !isRetired(r)))}>
                {t('sync_tasks.batch_retry')}
              </AsyncButton>
              <AsyncButton size="sm" variant="outline" className="text-destructive" disabled={batchBusy !== ''} pending={batchBusy === 'cancel'} onClick={() => batchCancel(ctx.loadedRows.filter(r => !isRetired(r)))}>
                {t('sync_tasks.batch_cancel')}
              </AsyncButton>
            </>
          ),
        }}
        card={r => (
          <RecordCard
            title={typeOf(r) ? t(`sync_tasks.type.${typeOf(r)}`) : '—'}
            status={<RowActions task={r} act={act} />}
            lines={[<StatusCell key="s" task={r} />, summaryOf(r), lastErrorOf(r)]}
          />
        )}
        detail={r => ({
          title: `${t('sync_tasks.detail_title')} #${idOf(r)}`,
          status: <StatusCell task={r} reason={false} />,
          actions: isRetired(r) ? undefined : (
            <>
              <AsyncButton variant="outline" size="sm" icon={<IconRefresh />} onClick={() => retry(r)}>{t('sync_tasks.action.retry')}</AsyncButton>
              <AsyncButton variant="outline" size="sm" className="text-destructive" onClick={() => cancel(r)}>{t('sync_tasks.action.cancel')}</AsyncButton>
            </>
          ),
          body: <TaskDetail task={r} />,
        })}
      />
    </div>
  )
}

/** One visible action (try again) and the rest in the menu; a retired task has neither to give. */
interface Act { retry: (r: SyncTask) => Promise<void>; cancel: (r: SyncTask) => Promise<void>; open: (id: number) => void }

function RowActions({ task, act }: { task: SyncTask; act: RefObject<Act> }) {
  const t = useAdminT()
  const retired = isRetired(task)
  const id = idOf(task)
  return (
    <div className="flex items-center justify-end gap-0.5">
      <AsyncIconButton label={t('sync_tasks.action.retry')} size="icon-xs" aria-disabled={retired || undefined} onClick={() => act.current.retry(task)}>
        <IconRefresh />
      </AsyncIconButton>
      <MoreMenu
        size="icon-xs"
        label={t('sync_tasks.action.row_menu', { id })}
        items={[
          { id: 'detail', label: t('sync_tasks.action.detail'), onSelect: () => act.current.open(id) },
          { id: 'cancel', label: t('sync_tasks.action.cancel'), destructive: true, disabled: retired, onSelect: () => void act.current.cancel(task) },
        ]}
      />
    </div>
  )
}
