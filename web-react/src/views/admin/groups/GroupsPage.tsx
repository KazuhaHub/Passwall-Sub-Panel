import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
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
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconDelete, IconUsers } from '@/lib/icons'
import { useGroupsList } from '@/query/groups'
import { groupKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'
import { RowActions } from '../subscription/RowActions'
import { RowCard } from '../subscription/RowCard'
import { GroupStatsContext, groupColumns, quotaSummary } from './columns'
import { GroupSheet, type GroupTarget } from './GroupSheet'
import { useGroupsT } from './useGroupsT'
import { useMatchCounts } from './useMatchCounts'
import { useOverrideCounts } from './useOverrideCounts'

const NONE: Group[] = []

/**
 * Groups: what a group's users get. The list shows what an admin scans for (members, the nodes and lines the filter
 * reaches, default limits, whether two-step verification is required, how many settings are overridden). One group
 * is edited in a wide sheet (GroupSheet), opened from the address (`?edit=<id>` or `?edit=new`) so it can be linked,
 * reloaded and closed with Back; the access-control page links to it with `&dtab=destination`.
 *
 * Only an administrator edits (config.write); an operator reads the list, without the two columns whose reads are
 * admin-only on the server.
 */
export default function GroupsPage() {
  const t = useGroupsT()
  const { t: tr } = useTranslation(['admin', 'common'])
  const navigate = useNavigate()
  const canConfig = useCan('config.write')
  const canAccess = useCan('access.view')
  const qScope = useQueryScope()
  const queryClient = useQueryClient()
  const groupsQuery = useGroupsList(qScope)
  const items = groupsQuery.data?.items ?? NONE
  const [q, setQ] = useUrlState('q', urlCodecs.string())
  const edit = useUrlSelection('edit')
  // The sheet's tab is in the address too (`dtab`); closing the sheet takes it away with `edit`.
  const [, setTab] = useUrlState('dtab', urlCodecs.string())
  const closeSheet = () => { edit.clear(); setTab('') }
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState<ReadonlySet<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const matches = useMatchCounts(qScope, items, canConfig)
  const overrides = useOverrideCounts(qScope, items, canConfig)

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

  /**
   * The server refuses to delete a group that still has members (409), so asking is pointless for those: say how many
   * users are in it and what to do (move them on the Users page), with a button that goes there. An empty group
   * confirms with what goes with it.
   */
  async function confirmDelete(g: Group) {
    if (g.members > 0) {
      const toUsers = await confirm({
        title: t('admin:groups.delete.blocked_title', { name: g.name }),
        message: t('admin:groups.delete.blocked_message', { count: g.members }),
        confirmText: t('admin:groups.delete.blocked_action'),
        cancelText: tr('common:actions.close'),
      })
      if (toUsers) void navigate('/admin/users')
      return
    }
    const ok = await confirm({
      title: t('admin:groups.delete.title', { name: g.name }),
      message: t('admin:groups.delete.message'),
      confirmText: tr('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
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
    t, canConfig, busy, onEdit: g => edit.select(String(g.id)), menuOf,
  // The row handlers close over the list they read; they are rebuilt with the columns when it changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [t, canConfig, busy, items])

  // Free-text filter on name / slug (the two identifiers). Remark is left out: a human-readable note, not something to match on.
  const shown = useMemo(() => filterRows(items, g => `${g.name} ${g.slug}`, q), [items, q])
  const filtered = q.trim() !== ''

  // What the sheet shows, from the address: an existing group's id, or `new`.
  const target: GroupTarget | null = useMemo(() => {
    if (!canConfig || !edit.selected) return null
    if (edit.selected === 'new') return { kind: 'new' }
    const found = items.find(g => String(g.id) === edit.selected)
    return found ? { kind: 'edit', group: found } : null
  }, [canConfig, edit.selected, items])

  if (groupsQuery.isError) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('admin:groups.title')} description={t('admin:groups.subtitle')} />
        <ErrorState title={t('admin:groups.load_failed')} reason={groupsQuery.error} retry={() => load()} />
      </div>
    )
  }

  const loading = groupsQuery.isPending
  const stats = { matches, overrides }
  return (
    <GroupStatsContext.Provider value={stats}>
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:groups.title')}
        count={loading ? undefined : items.length}
        description={t('admin:groups.subtitle')}
        primary={canConfig && <Button onClick={() => edit.select('new')}>{t('admin:groups.create')}</Button>}
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
        onRowOpen={canConfig ? g => edit.select(String(g.id)) : undefined}
        activeRowId={edit.selected && edit.selected !== 'new' ? edit.selected : null}
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
            actions={canConfig ? <RowActions name={g.name} editLabel={t('admin:groups.edit_title')} menuLabel={t('admin:groups.row_menu', { name: g.name })} busy={busy.has(g.id)} onEdit={() => edit.select(String(g.id))} menu={menuOf(g)} /> : undefined} />
        )}
        toolbar={(
          <FilterBar
            search={{ value: q, onChange: setQ, placeholder: t('admin:groups.search_placeholder') }}
            onClearAll={() => setQ('')}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:groups.no_match')} action={<Button variant="outline" onClick={() => setQ('')}>{t('admin:groups.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:groups.empty')} description={t('admin:groups.empty_hint')} action={canConfig ? <Button onClick={() => edit.select('new')}>{t('admin:groups.create')}</Button> : undefined} />}
        bulkActions={canConfig ? (ctx => {
          const rows = items.filter(i => ctx.ids.includes(String(i.id)))
          return (
            <Button variant="outline" size="sm" disabled={batchBusy} className="text-destructive" onClick={() => void batchDelete(rows, ctx.hiddenCount, ctx.clear)}>
              <IconDelete />{t('admin:groups.batch_delete')}
            </Button>
          )
        }) : undefined}
      />
      <GroupSheet
        target={target}
        canAccess={canAccess}
        onClose={closeSheet}
        onSaved={(saved, wasEdit, closes) => {
          if (wasEdit && saved) mutateItems(prev => prev.map(item => (item.id === saved.id ? saved : item)))
          else void load().catch(() => {})
          if (closes) closeSheet()
        }}
      />
    </div>
    </GroupStatsContext.Provider>
  )
}
