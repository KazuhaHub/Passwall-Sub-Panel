import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestBudgetItem, type DestList, type DestListKind, type DestPolicyRef } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { DataTable } from '@/components/data/DataTable'
import type { SortingState } from '@/components/data/dataTableTypes'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { pushSnack } from '@/components/feedback/toasts'
import { LevelAlert } from '@/components/LevelAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconAdd, IconChevronDown, IconWarning } from '@/lib/icons'
import { useDeleteDestList, useDestLists, useDestPolicies, useGeositeStatus, useRefreshDestList, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { budgetLevel } from '@/utils/accessControl'
import { parseListKindFilter, parseListStateFilter, type AccessNavState, type SettingsFocus } from '../accessParams'
import { accessReturnState, listPath, newListPath } from '../accessRoutes'
import { deleteListConfirm } from '../confirmCopy'
import { destErrorText, reportDestError } from '../destErrors'
import { ListCard, listColumns, type ListColumnCtx } from './listColumns'
import { listStatus } from './listStatus'

const L = 'admin:access_control.lists.'

export interface ListsTabProps {
  /** Opens the entries sheet (`sheet=list&list=<id>`). */
  onOpenEntries: (id: number) => void
  /** Opens the settings dialog, focused on a field. */
  onOpenSettings: (focus?: SettingsFocus) => void
  /** Switches to the policies tab and opens that policy's editor. */
  onOpenPolicy: (id: number) => void
  /** Opens an allowlist group's card on the allowlist tab. */
  onOpenAllowlist: (groupId: number) => void
}

/**
 * THE LISTS TAB (S6): how much of each quota the lists use, which list is
 * broken (and still serving its old content), and who uses each one. The
 * filters are in the URL (`lst_state`, `lst_kind`); sorting is the page's own.
 * A list something uses cannot be deleted (the menu says by whom); a delete the
 * server refuses anyway (409) is explained with links, not asked again.
 */
export default function ListsTab({ onOpenEntries, onOpenSettings, onOpenPolicy, onOpenAllowlist }: ListsTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const location = useLocation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const lists = useDestLists(scope)
  const policies = useDestPolicies(scope)
  const del = useDeleteDestList(scope)
  const refresh = useRefreshDestList(scope)
  const download = useUpdateGeosite(scope)
  const geosite = useGeositeStatus(scope)
  const stateFilter = parseListStateFilter(params.get('lst_state'))
  const kindFilter = parseListKindFilter(params.get('lst_kind'))
  const [sorting, setSorting] = useState<SortingState>([{ id: 'name', desc: false }])
  const [inUse, setInUse] = useState<{ name: string; usedBy: DestPolicyRef[] } | null>(null)

  /** The editor and the create form are pages; they carry this tab's place so their back link returns to it. */
  const openPage = (to: string, replace = false) => void navigate(to, { replace, state: accessReturnState(location.pathname, location.search) })

  // Another part of the page asked for the editor on arrival (state, never the URL): it is a page now, so go there,
  // replacing this entry so Back does not return to the redirect. Keyed by the history entry, so a second request is handled too.
  const handled = useRef<string | null>(null)
  useEffect(() => {
    const want = (location.state as AccessNavState | null)?.openList
    if (want === undefined || handled.current === location.key) return
    handled.current = location.key
    void navigate(typeof want === 'number' ? listPath(want) : newListPath(want.replace('new_', '') as DestListKind), {
      replace: true, state: accessReturnState(location.pathname, location.search),
    })
  }, [location, navigate])

  const enabledIds = useMemo(
    () => new Set([...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].filter(p => p.state !== 'off').map(p => p.id)),
    [policies.data])
  const items = lists.data?.items
  const usedEnabled = (l: DestList) => l.used_by.some(u => u.kind === 'policy' && enabledIds.has(u.id))
  const problems = (items ?? []).filter(l => listStatus(l, usedEnabled(l)).problem).length

  const visible = useMemo(() => {
    let out = (items ?? []).filter(l => !kindFilter || l.kind === kindFilter)
    if (stateFilter === 'problem') out = out.filter(l => listStatus(l, l.used_by.some(u => u.kind === 'policy' && enabledIds.has(u.id))).problem)
    return out
  }, [items, kindFilter, stateFilter, enabledIds])

  const setFilter = (key: string, value: string | null) => setParams(prev => {
    const out = new URLSearchParams(prev)
    if (value) out.set(key, value)
    else out.delete(key)
    return out
  }, { replace: true })

  const doDelete = async (list: DestList) => {
    if (!(await confirm({ ...deleteListConfirm(t, list.name), initialFocus: 'cancel' }))) return
    try {
      await del.mutateAsync(list.id)
      pushSnack(t(`${L}snack.deleted`, { name: list.name }), 'success')
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_list_in_use') setInUse({ name: list.name, usedBy: (e.body.used_by as DestPolicyRef[] | undefined) ?? list.used_by })
      else reportDestError(t, err)
    }
  }
  const doRefresh = async (list: DestList) => {
    try {
      await refresh.mutateAsync(list.id)
      pushSnack(t(`${L}snack.refresh_started`, { name: list.name }), 'info')
    } catch (err) { reportDestError(t, err) }
  }

  // The columns are one stable array (DataTable memoises its rows on them); the handlers they call are read
  // through a ref, so the latest render's closures run without rebuilding the columns.
  const h = useRef({ onOpenEntries, onOpenPolicy, onOpenAllowlist, doDelete, doRefresh, openPage })
  h.current = { onOpenEntries, onOpenPolicy, onOpenAllowlist, doDelete, doRefresh, openPage }
  const ctx = useMemo<ListColumnCtx>(() => ({
    t, lang: i18n.language,
    usedEnabled: l => l.used_by.some(u => u.kind === 'policy' && enabledIds.has(u.id)),
    onOpenEntries: id => h.current.onOpenEntries(id),
    onEdit: id => h.current.openPage(listPath(id)),
    onRefresh: l => void h.current.doRefresh(l),
    onDelete: l => void h.current.doDelete(l),
    onOpenPolicy: id => h.current.onOpenPolicy(id),
    onOpenAllowlist: id => h.current.onOpenAllowlist(id),
  }), [t, i18n.language, enabledIds])
  const columns = useMemo(() => listColumns(ctx), [ctx])

  const num = new Intl.NumberFormat(i18n.language)
  const tile = (label: string, b: DestBudgetItem | undefined) => {
    const level = budgetLevel(b)
    return (
      <KpiTile label={label} value={b ? num.format(b.used) : '—'} tone={level === 'over' ? 'danger' : level === 'near' ? 'warning' : undefined}
        caption={b ? <>{t(`${L}kpi.of`, { limit: num.format(b.limit) })}{level === 'near' && <span data-level="near"> ▲</span>}</> : undefined} />
    )
  }
  const overQuota = lists.data ? (Object.values(lists.data.budget) as DestBudgetItem[]).some(b => budgetLevel(b) === 'over') : false

  const newMenu = (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button />}><IconAdd />{t(`${L}new`)}<IconChevronDown /></DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {(['custom', 'remote', 'geosite'] as const).map(k => (
          <DropdownMenuItem key={k} onClick={() => openPage(newListPath(k))}>{t(`${L}kind.${k}`)}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )

  const unwired = lists.isError && isDestUnavailable(lists.error)
  let body
  if (lists.isPending) {
    body = <TableSkeleton rows={5} cols={5} />
  } else if (lists.isError) {
    body = unwired
      ? <LevelAlert level="info">{t('admin:access_control.unwired')}</LevelAlert>
      : <ErrorState title={destErrorText(t, lists.error)} retry={() => lists.refetch()} />
  } else if ((items ?? []).length === 0) {
    body = <EmptyState title={t(`${L}empty`)} action={newMenu} />
  } else if (visible.length === 0) {
    body = (
      <EmptyState title={t(`${L}empty_filtered`)}
        action={<Button variant="outline" onClick={() => setParams(prev => { const o = new URLSearchParams(prev); o.delete('lst_state'); o.delete('lst_kind'); return o }, { replace: true })}>{t(`${L}clear_filter`)}</Button>} />
    )
  } else {
    body = (
      <DataTable<DestList>
        aria-label={t('admin:access_control.tab_lists')}
        data={visible} columns={columns} getRowId={l => String(l.id)} rowLabel={l => l.name}
        pagination={false} columnMenu={false}
        sorting={sorting} onSortingChange={setSorting}
        onRowOpen={l => onOpenEntries(l.id)}
        renderCard={(l, c) => <ListCard list={l} ctx={ctx} open={c.open} />}
      />
    )
  }

  return (
    <div>
      <div className="mb-3 flex justify-end">{(items ?? []).length > 0 && newMenu}</div>
      {lists.data && (
        <>
          <KpiGrid>
            {tile(t(`${L}kpi.domains`), lists.data.budget.domains)}
            {tile(t(`${L}kpi.regexps`), lists.data.budget.regexps)}
            {tile(t(`${L}kpi.cidrs`), lists.data.budget.cidrs)}
            <KpiTile label={t(`${L}kpi.problems`)} value={num.format(problems)} caption={t(`${L}kpi.problems_caption`)}
              danger={problems > 0} pressed={stateFilter === 'problem'}
              onToggle={() => setFilter('lst_state', stateFilter === 'problem' ? null : 'problem')} />
          </KpiGrid>
          {overQuota && <p className="mb-2 text-[13px] text-destructive">{t(`${L}kpi.over_quota`)}</p>}
          <p className="mb-3 text-[13px] text-muted-foreground">
            {t(`${L}refresh_note`, { hours: lists.data.refresh_hours })}{' '}
            <button type="button" className="text-foreground underline underline-offset-2" onClick={() => onOpenSettings('refresh')}>{t(`${L}change`)}</button>
          </p>
        </>
      )}
      {lists.isRefetchError && lists.data && (
        <p className="mb-2 flex items-center gap-1 text-xs"><IconWarning aria-hidden className="size-3.5 text-warning-ink" />{t('admin:access_control.verdict.stale')}</p>
      )}
      {geosite.data && !geosite.data.current && (
        <LevelAlert level="info" className="mb-3">
          <span className="flex flex-wrap items-center justify-between gap-2">
            <span>{t(`${L}geosite_missing`)}</span>
            <AsyncButton size="sm" variant="outline" onClick={async () => {
              try { await download.mutateAsync({}); pushSnack(t(`${L}snack.downloaded`), 'success') } catch (err) { reportDestError(t, err) }
            }}>{t(`${L}geosite_download`)}</AsyncButton>
          </span>
        </LevelAlert>
      )}
      {body}
      <Dialog open={inUse !== null} onOpenChange={o => { if (!o) setInUse(null) }}>
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{t('admin:access_control.confirm.list_in_use.title')}</DialogTitle>
            <DialogDescription>{t('admin:access_control.confirm.list_in_use.message')}</DialogDescription>
          </DialogHeader>
          <ul className="m-0 grid list-none gap-1 p-0 text-sm">
            {(inUse?.usedBy ?? []).map(u => (
              <li key={`${u.kind}:${u.id}`}>
                {u.kind === 'route'
                  ? <span data-testid="in-use-route">{u.name || String(u.id)}</span>
                  : <button type="button" className="underline underline-offset-2" onClick={() => { setInUse(null); if (u.kind === 'group') onOpenAllowlist(u.id); else onOpenPolicy(u.id) }}>{u.name || String(u.id)}</button>}
              </li>
            ))}
          </ul>
          <DialogFooter><Button onClick={() => setInUse(null)}>{t('admin:access_control.confirm.list_in_use.action')}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
