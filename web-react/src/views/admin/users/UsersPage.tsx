import { useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useLocation } from 'react-router'
import { runReconcile } from '@/api/reconcile'
import type { User } from '@/api/types'
import { AsyncButton } from '@/components/common/AsyncButton'
import { MoreMenu } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { DataTable } from '@/components/data/DataTable'
import type { BulkContext } from '@/components/data/dataTableTypes'
import { FilterBar } from '@/components/data/FilterBar'
import { pushSnack } from '@/components/feedback/toasts'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useObjectLink } from '@/components/common/objectReturn'
import { IconDelete, IconRefresh, IconSync } from '@/lib/icons'
import { useUsersList } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { accountStateOf, proxyEnabled } from '@/utils/userAccess'
import RiskUserDialog from '../risk/dialog/RiskUserDialog'
import { singleFacet } from '../logs/shared'
import { UserCard } from './cells'
import { userColumns } from './columns'
import { UsersContext } from './context'
import { canManageUser, canSelect } from './model'
import { RowActions } from './RowActions'
import { UserDialogs } from './UserDialogs'
import { legacyUserTarget, NEW_USER_PATH } from './userRoutes'
import { useAdminT } from './useAdminT'
import { useBatchActions } from './useBatchActions'
import { useUsersWorkspace } from './useUsersWorkspace'
import { useUserListView } from './useUserListView'

/**
 * The Users list: a server-paged table an admin scans (who, group, status, traffic against quota, expiry, last
 * seen) and the bulk bar. A user is NOT opened here: the name, the row and the edit button go to the user's own page
 * (`/admin/users/:id`), carrying this list's place so its back link returns to the same page, search and order.
 * Everything that is a place is in the URL: the page, the keyword, the order, the group filter, the risk drawer.
 *
 * `?user=<id>` / `?user=new` are the addresses the old side sheet used; they are sent on to the new routes (see
 * `legacyUserTarget`) because bookmarks and other pages still carry them.
 */
export default function UsersPage() {
  const legacy = legacyUserTarget(useLocation().search)
  return legacy ? <Navigate to={legacy} replace /> : <UsersList />
}

function UsersList() {
  const t = useAdminT()
  const scope = useQueryScope()
  const link = useObjectLink()
  const view = useUserListView()
  const { ctx, dialog, setDialog, riskDrawer, canRisk } = useUsersWorkspace({ usageLimit: view.pageSize })
  const { admin, canElevate } = ctx

  const usersQuery = useUsersList(scope, view.params)
  const items = usersQuery.data?.items
  const total = usersQuery.data?.total
  const rows = items ?? []

  const [selection, setSelection] = useState<Record<string, boolean>>({})

  const { act, groups, usedBytesOf, reload } = ctx
  const batch = useBatchActions({ reload, invalidateUsage: act.invalidateUsage, setDialog, clearSelection: () => setSelection({}) })

  // A batch is assembled on one page of one query: reset when THAT changes, not whenever the rows arrive as a new array, which dropped a batch the admin was still putting together on every background refresh.
  useEffect(() => { setSelection({}) }, [view.page, view.pageSize, view.q, view.params.sort_by, view.params.sort_dir, view.group])
  // Independently, drop ids that left the page or stopped being actionable, so a batch can never target a row the admin can no longer act on.
  useEffect(() => {
    if (!items) return
    const valid = new Set(items.filter(u => canSelect(u) && canManageUser(u, canElevate)).map(u => String(u.id)))
    setSelection(prev => {
      const keep = Object.keys(prev).filter(id => prev[id] && valid.has(id))
      return keep.length === Object.keys(prev).filter(id => prev[id]).length ? prev : Object.fromEntries(keep.map(id => [id, true]))
    })
  }, [items, canElevate])

  const [reconciling, setReconciling] = useState(false)
  async function reconcile() {
    setReconciling(true)
    try {
      const report = await runReconcile()
      // Whenever something happened (a fix OR an issue left), not just for leftovers: an admin wants to see what got fixed.
      if ((report.fixed ?? 0) > 0 || (report.issues?.length ?? 0) > 0) setDialog({ kind: 'reconcile', report })
      else pushSnack(t('users.reconcile.summary_no_fix', { scanned: report.scanned }), 'success')
      await reload()
    } catch { /* reported by the request layer */ } finally { setReconciling(false) }
  }

  // Columns depend on the language and the role only; what the cells draw arrives through the context (see columns.tsx).
  const columns = useMemo(() => userColumns({ t, canRisk }), [t, canRisk])

  const group = singleFacet<string>(view.group > 0 ? String(view.group) : '', v => view.setGroup(v ? Number(v) : 0))
  const filtered = view.narrowed

  if (usersQuery.isError && !usersQuery.data) {
    return <div className="p-4 sm:p-6"><ErrorState title={t('users.load_failed')} reason={usersQuery.error} retry={() => usersQuery.refetch()} /></div>
  }

  const bulk = (c: BulkContext<User>) => {
    const sel = c.loadedRows
    const off = batch.busy !== ''
    return (
      <>
        <AsyncButton size="sm" variant="outline" disabled={off} pending={batch.busy === 'enable'} onClick={() => batch.askToggleAccount(sel, true)}>{t('users.batch.enable')}</AsyncButton>
        <AsyncButton size="sm" variant="outline" disabled={off} pending={batch.busy === 'disable'} onClick={() => batch.askToggleAccount(sel, false)}>{t('users.batch.disable')}</AsyncButton>
        <AsyncButton size="sm" variant="outline" disabled={off} pending={batch.busy === 'renew'} onClick={() => batch.renew(sel)}>{t('users.batch.renew')}</AsyncButton>
        <AsyncButton size="sm" variant="outline" className="text-destructive" icon={<IconDelete />} disabled={off} pending={batch.busy === 'delete'} onClick={() => batch.remove(sel)}>{t('users.batch.delete')}</AsyncButton>
        <MoreMenu size="icon-sm" label={t('users.batch.more')} items={[
          { id: 'emergency', label: t('users.more_menu.reset_emergency'), disabled: off, onSelect: () => void batch.emergency(sel) },
          { id: 'unlink', label: t('users.batch.unlink_sso'), disabled: off, onSelect: () => void batch.unlinkSso(sel) },
        ]} />
      </>
    )
  }

  const empty = filtered
    ? <EmptyState compact title={t('users.empty_filtered')} action={<Button variant="outline" size="sm" onClick={view.clearFilters}>{t('logs.clear_filters')}</Button>} />
    : <EmptyState compact title={t('users.empty')} description={t('users.empty_hint')} action={<Button size="sm" render={<Link {...link(NEW_USER_PATH)} />}>{t('users.create')}</Button>} />

  return (
    <UsersContext.Provider value={ctx}>
      <div className="p-4 sm:p-6">
        <PageHeader
          title={t('users.title')}
          count={total}
          primary={<Button render={<Link {...link(NEW_USER_PATH)} />}>{t('users.create')}</Button>}
          // isFetching, not isPending: it is false again once the first page has loaded, even mid-refetch, and on a slow link the click must visibly go busy.
          secondary={<AsyncButton variant="outline" icon={<IconRefresh />} pending={usersQuery.isFetching} onClick={() => usersQuery.refetch()}>{t('users.refresh')}</AsyncButton>}
          // Reconcile is an admin endpoint: for an operator it would only answer 403, so it is not offered.
          more={admin ? [{ id: 'reconcile', label: t('users.reconcile.trigger'), icon: IconSync, disabled: reconciling, onSelect: () => void reconcile() }] : []}
        />
        {usersQuery.isError && (
          // The rows below are from the last read, not a clean list: say so rather than staying silent.
          <Alert variant="destructive" className="mb-3"><AlertDescription>{t('users.refresh_failed')}</AlertDescription></Alert>
        )}
        <DataTable<User>
          tableId="admin.users"
          aria-label={t('users.title')}
          mode="server"
          data={rows}
          total={total}
          columns={columns}
          getRowId={u => String(u.id)}
          loading={usersQuery.isPending}
          page={view.page}
          onPageChange={view.setPage}
          pageSize={view.pageSize}
          onPageSizeChange={view.setPageSize}
          sorting={view.sorting}
          onSortingChange={view.setSorting}
          columnVisibility={view.columnVisibility}
          onColumnVisibilityChange={view.setColumnVisibility}
          selectable
          selection={selection}
          onSelectionChange={setSelection}
          rowLabel={u => u.upn}
          bulkActions={bulk}
          // Out of every batch: an account waiting for deletion, and (for an operator) one they may not touch.
          rowOptions={u => ({
            selectable: canSelect(u) && canManageUser(u, canElevate),
            state: act.busy.has(u.id) ? 'pending' : accountStateOf(u) === 'active' && proxyEnabled(u) ? undefined : 'dimmed',
          })}
          onRowOpen={u => ctx.openUser(u.id)}
          renderCard={u => <UserCard user={u} usedBytes={usedBytesOf(u.id)} actions={<RowActions user={u} />} />}
          maxHeight={null}
          toolbar={(
            <FilterBar
              aria-label={t('users.title')}
              search={{ value: view.q, onChange: view.setQ, placeholder: t('users.search_placeholder') }}
              facets={[{ id: 'group', label: t('users.field.group'), options: groups.map(g => ({ value: String(g.id), label: g.name })), ...group }]}
              onClearAll={view.clearFilters}
            />
          )}
          empty={empty}
        />
        <UserDialogs state={dialog} setState={setDialog} reload={reload} toggleBatch={batch.toggleAccount} />
        {/* Above every modal (riskDrawerZIndex). Not mounted for an operator: a link to ?risk= would only read a 403. */}
        {canRisk && <RiskUserDialog userId={riskDrawer.id} onClose={riskDrawer.close} host="users" />}
      </div>
    </UsersContext.Provider>
  )
}
