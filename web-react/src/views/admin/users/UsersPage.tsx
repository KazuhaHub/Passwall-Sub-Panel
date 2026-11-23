import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { runReconcile } from '@/api/reconcile'
import type { ListResponse, User } from '@/api/types'
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
import { useDrawerParam } from '@/hooks/useDrawerParam'
import { IconDelete, IconRefresh, IconSync } from '@/lib/icons'
import { useAllGroups } from '@/query/groups'
import { userKeys } from '@/query/keys'
import { useRiskLevels } from '@/query/riskCenter'
import { useTopTraffic } from '@/query/traffic'
import { useUsersList } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { useAuthStore } from '@/stores/auth'
import { useCan } from '@/utils/permissions'
import { accountStateOf, proxyEnabled } from '@/utils/userAccess'
import RiskUserDrawer from '../risk/drawer/RiskUserDrawer'
import { singleFacet } from '../logs/shared'
import { UserCard } from './cells'
import { userColumns } from './columns'
import { UsersContext, type UsersCtx } from './context'
import type { DialogState } from './dialogs'
import { canManageUser, canSelect } from './model'
import { RowActions } from './RowActions'
import { UserDialogs } from './UserDialogs'
import { UserSheet } from './UserSheet'
import { useAdminT } from './useAdminT'
import { useBatchActions } from './useBatchActions'
import { useUserActions } from './useUserActions'
import { useUserListView } from './useUserListView'
import { useUserSheet } from './useUserSheet'

const NO_GROUPS: never[] = []

/**
 * The Users page: a server-paged list an admin scans (who, group, status, traffic against quota, expiry, last
 * seen), one user in a wide sheet, and the bulk bar. Everything that is a place is in the URL: the page, the
 * keyword, the order, the group filter, the opened user and its tab, the risk drawer.
 */
export default function UsersPage() {
  const t = useAdminT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const auth = useAuthStore()
  const admin = useCan('config.write')
  const canElevate = useCan('users.elevate')
  // The risk center is admin-only: an operator is shown neither its column nor its drawer, and never reads /levels, which would answer 403.
  const canRisk = useCan('risk.view')
  const view = useUserListView()
  const sheet = useUserSheet()
  // The risk center's drawer over this page (`?risk=<id>`): a copied link reopens it and Back closes it. Opening it from the sheet hands over to it (the sheet's own params go), and closing it comes back to the sheet.
  const riskDrawer = useDrawerParam('risk', { exclusive: ['user', 'tab'] })

  const usersQuery = useUsersList(scope, view.params)
  const items = usersQuery.data?.items
  const total = usersQuery.data?.total
  const rows = items ?? []

  const groups = useAllGroups(scope).data ?? NO_GROUPS
  // The risk column: ONE read for the whole list, keyed by account id. Decoration only: a failed read leaves it blank and the list works as before.
  const levels = useRiskLevels(scope, { enabled: canRisk }).data
  // Per-user period usage, from the Top-N leaderboard through the shared cache. It walks every user server-side, so it never rides the list refresh; `limit` tracks the page size.
  const top = useTopTraffic(scope, Math.max(view.pageSize, 25))
  const usage = useMemo(() => new Map((top.data ?? []).map(r => [r.user_id, r.period_used_bytes])), [top.data])
  const usedBytesOf = (id: number) => usage.get(id)

  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [selection, setSelection] = useState<Record<string, boolean>>({})

  /** Re-reads the list, and the opened user when it is not on this page. */
  const sheetId = typeof sheet.target === 'number' ? sheet.target : 0
  const reload = async () => {
    await Promise.all([
      usersQuery.refetch(),
      sheetId > 0 ? queryClient.invalidateQueries({ queryKey: userKeys.detail(scope, sheetId) }) : undefined,
    ])
  }
  /** A saved row goes into the cached list and detail at once, so nothing shows the stale one while a refetch is under way. */
  const patchRow = (u: User) => {
    queryClient.setQueryData<ListResponse<User>>(userKeys.list(scope, view.params), prev => (prev ? { ...prev, items: prev.items.map(x => (x.id === u.id ? u : x)) } : prev))
    queryClient.setQueryData(userKeys.detail(scope, u.id), u)
  }

  const act = useUserActions({ reload, setDialog, afterDelete: id => { if (sheet.target === id) sheet.exit() } })
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

  const ctx: UsersCtx = {
    act, groups, usedBytesOf, levels, selfId: auth.userId, role: auth.role, canElevate, canRisk, admin, reload, patchRow,
    openRisk: id => riskDrawer.open(id), openUser: (id, tab) => sheet.open(id, tab), setDialog,
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
    : <EmptyState compact title={t('users.empty')} description={t('users.empty_hint')} action={<Button size="sm" onClick={() => sheet.open('new')}>{t('users.create')}</Button>} />

  return (
    <UsersContext.Provider value={ctx}>
      <div className="p-4 sm:p-6">
        <PageHeader
          title={t('users.title')}
          count={total}
          primary={<Button onClick={() => sheet.open('new')}>{t('users.create')}</Button>}
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
          onRowOpen={u => sheet.open(u.id)}
          activeRowId={typeof sheet.target === 'number' ? String(sheet.target) : null}
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
        <UserSheet sheet={sheet} rows={rows} loading={usersQuery.isPending} />
        <UserDialogs state={dialog} setState={setDialog} reload={reload} toggleBatch={batch.toggleAccount} />
        {/* Above every modal (riskDrawerZIndex). Not mounted for an operator: a link to ?risk= would only read a 403. */}
        {canRisk && <RiskUserDrawer userId={riskDrawer.id} onClose={riskDrawer.close} host="users" />}
      </div>
    </UsersContext.Provider>
  )
}
