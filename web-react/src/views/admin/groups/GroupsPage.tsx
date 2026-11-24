import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import { deleteGroup } from '@/api/groups'
import type { Group, ListResponse } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { StatusDot } from '@/components/common/StatusDot'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { filterRows } from '@/components/data/dataTableLogic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { useObjectLink } from '@/components/common/objectReturn'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconDelete, IconUsers } from '@/lib/icons'
import { useGroupsList } from '@/query/groups'
import { groupKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'
import { RowActions } from '../subscription/RowActions'
import { RowCard } from '../subscription/RowCard'
import { groupColumns, quotaSummary } from './columns'
import { confirmDeleteGroup } from './groupActions'
import { groupPath, legacyGroupTarget, NEW_GROUP_PATH } from './groupRoutes'
import { useGroupsT } from './useGroupsT'

const NONE: Group[] = []

/**
 * Groups: what a group's users get. The list shows what an admin scans for (members, the nodes and lines the filter
 * reaches, default limits, whether two-step verification is required, how many settings are overridden). A group is
 * NOT opened here: the row and its edit button go to the group's own page (`/admin/groups/:id`, tabs in `?tab=`),
 * carrying this list's place so its back link returns to the same search. `?edit=<id>` / `?edit=new` (`&dtab=`) are
 * the addresses the old side sheet used; an administrator is sent on to the new routes (bookmarks and the
 * access-control page still carried them). An operator, who was never shown the sheet, just sees the list.
 *
 * Only an administrator edits (config.write); an operator reads the list, without the two columns whose reads are
 * admin-only on the server.
 */
export default function GroupsPage() {
  const canConfig = useCan('config.write')
  const legacy = legacyGroupTarget(useLocation().search)
  return legacy && canConfig ? <Navigate to={legacy} replace /> : <GroupsList />
}

function GroupsList() {
  const t = useGroupsT()
  const link = useObjectLink()
  const { t: tr } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const canConfig = useCan('config.write')
  const qScope = useQueryScope()
  const queryClient = useQueryClient()
  const groupsQuery = useGroupsList(qScope)
  const items = groupsQuery.data?.items ?? NONE
  const [q, setQ] = useUrlState('q', urlCodecs.string())
  const open = (g: Group) => { const l = link(groupPath(g.id)); void navigate(l.to, { state: l.state }) }
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)

  /** Patch the cached list after a write, so the table reflects it immediately instead of waiting for the next read. */
  function mutateItems(updater: (prev: Group[]) => Group[]) {
    queryClient.setQueryData<ListResponse<Group>>(groupKeys.list(qScope, {}), prev => (prev ? { ...prev, items: updater(prev.items) } : prev))
  }
  // Reload after a write. Clearing the selection is deliberate (the rows it referred to may be gone).
  function load() {
    setChecked({})
    return groupsQuery.refetch()
  }
  const withBusy = async (id: number, run: () => Promise<void>) => {
    setBusy(prev => new Set(prev).add(id))
    try { await run() } catch { /* The request layer already surfaced the failure. */ } finally {
      setBusy(prev => { const next = new Set(prev); next.delete(id); return next })
    }
  }

  async function confirmDelete(g: Group) {
    if (!(await confirmDeleteGroup(g, tr, navigate))) return
    await withBusy(g.id, async () => {
      await deleteGroup(g.id)
      pushToast({ message: t('admin:groups.toast.deleted'), severity: 'success' })
      await load()
    })
  }

  async function batchDelete(rows: Group[], hidden: number, clear: () => void) {
    if (!rows.length) return
    const names = rows.slice(0, 5).map(r => r.name).join('、')
    const suffix = rows.length > 5 ? ` +${rows.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:groups.confirm.batch_delete_title'),
      message: t('admin:groups.confirm.batch_delete_message', { names, suffix }) + (hidden > 0 ? ` ${t('admin:groups.confirm.batch_delete_hidden', { count: hidden })}` : ''),
      confirmText: tr('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy(true)
    try {
      const results = await allSettledLimited(rows, r => deleteGroup(r.id))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      mutateItems(prev => prev.filter(g => !okIds.includes(g.id)))
      clear()
      if (failed > 0) pushToast({ message: t('admin:groups.toast.batch_partial', { ok: okIds.length, fail: failed }), severity: 'warning' })
      else pushToast({ message: t('admin:groups.toast.batch_deleted', { count: okIds.length }), severity: 'success' })
    } finally { setBatchBusy(false) }
  }

  function menuOf(g: Group): MoreMenuItem[] {
    const wait = busy.has(g.id)
    return [
      { id: 'members', label: t('admin:groups.action.members'), icon: IconUsers, onSelect: () => void navigate('/admin/users') },
      { id: 'delete', label: t('admin:groups.action.delete'), icon: IconDelete, destructive: true, disabled: wait, onSelect: () => void confirmDelete(g) },
    ]
  }

  const columns = useMemo(() => groupColumns({
    t, canConfig, busy, onEdit: open, menuOf,
  // The row handlers close over the list they read; they are rebuilt with the columns when it changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [t, canConfig, busy, items])

  // Free-text filter on name / slug (the two identifiers). Remark is left out: a human-readable note, not something to match on.
  const shown = useMemo(() => filterRows(items, g => `${g.name} ${g.slug}`, q), [items, q])
  const filtered = q.trim() !== ''

  if (groupsQuery.isError) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('admin:groups.title')} description={t('admin:groups.subtitle')} />
        <ErrorState title={t('admin:groups.load_failed')} reason={groupsQuery.error} retry={() => load()} />
      </div>
    )
  }

  const loading = groupsQuery.isPending
  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:groups.title')}
        count={loading ? undefined : items.length}
        description={t('admin:groups.subtitle')}
        primary={canConfig && <Button render={<Link {...link(NEW_GROUP_PATH)} />}>{t('admin:groups.create')}</Button>}
      />
      <DataTable<Group>
        aria-label={t('admin:groups.title')}
        data={shown}
        columns={columns}
        getRowId={g => String(g.id)}
        rowLabel={g => g.name}
        loading={loading}
        selectable={canConfig}
        selection={checked}
        onSelectionChange={setChecked}
        onRowOpen={canConfig ? open : undefined}
        // Only a group with no members can be deleted, so only those can be ticked.
        rowOptions={g => ({ selectable: g.members === 0, state: busy.has(g.id) ? 'pending' : undefined })}
        renderCard={(g, ctx) => (
          <RowCard title={g.name} subtitle={g.slug} open={ctx.open}
            status={g.require_2fa ? <StatusDot tone="info" label={t('admin:groups.list.two_factor_required')} /> : undefined}
            detail={[
              `${t('admin:groups.table.members')}: ${g.members}`,
              g.tag_filter.all ? t('admin:groups.tag.all') : (g.tag_filter.tags ?? []).join(' · '),
              quotaSummary(t, g),
            ].filter(Boolean).join(' · ')}
            actions={canConfig ? <RowActions name={g.name} editLabel={t('admin:groups.edit_title')} menuLabel={t('admin:groups.row_menu', { name: g.name })} busy={busy.has(g.id)} onEdit={() => open(g)} menu={menuOf(g)} /> : undefined} />
        )}
        toolbar={(
          <FilterBar
            search={{ value: q, onChange: setQ, placeholder: t('admin:groups.search_placeholder') }}
            onClearAll={() => setQ('')}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:groups.no_match')} action={<Button variant="outline" onClick={() => setQ('')}>{t('admin:groups.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:groups.empty')} description={t('admin:groups.empty_hint')} action={canConfig ? <Button render={<Link {...link(NEW_GROUP_PATH)} />}>{t('admin:groups.create')}</Button> : undefined} />}
        bulkActions={canConfig ? (ctx => {
          const rows = items.filter(i => ctx.ids.includes(String(i.id)))
          return (
            <Button variant="outline" size="sm" disabled={batchBusy} className="text-destructive" onClick={() => void batchDelete(rows, ctx.hiddenCount, ctx.clear)}>
              <IconDelete />{t('admin:groups.batch_delete')}
            </Button>
          )
        }) : undefined}
      />
    </div>
  )
}
