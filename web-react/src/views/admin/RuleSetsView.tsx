import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import { deleteRuleSet, resetRuleSet, ruleSetFailureOf, saveRuleSet, type RuleSet } from '@/api/rules'
import type { ListResponse } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { useObjectLink } from '@/components/common/objectReturn'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { filterRows } from '@/components/data/dataTableLogic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconDelete } from '@/lib/icons'
import { ruleKeys } from '@/query/keys'
import { useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'
import { askDeleteRuleSet, askResetRuleSet, isSeeded } from './rulesets/actions'
import { ruleSetColumns } from './rulesets/columns'
import { countLines } from './rulesets/model'
import { RowActions } from './subscription/RowActions'
import { RowCard } from './subscription/RowCard'
import { StatusDot } from '@/components/common/StatusDot'
import { isReservedSlug, legacyRuleSetTarget, newRuleSetPath, ruleSetPath } from './rulesets/ruleSetRoutes'

const NONE: never[] = []
class MissingRevision extends Error {}

/**
 * Rule sets: reusable snippets of routing rules that only take part in a subscription when a template binds them.
 * The list is here; a rule set is NOT opened here: the row, the edit button and "New rule set" go to the rule set's own
 * page (rulesets/RuleSetRoute). `?edit=<slug>` / `?edit=new[&copy=<slug>]` are the addresses the old side sheet used; they
 * are sent on to the new routes (see `legacyRuleSetTarget`) because bookmarks still carry them. An operator can open a
 * rule set (the server lets staff read them) and sees it without the means to change it.
 */
export default function RuleSetsView() {
  const legacy = legacyRuleSetTarget(useLocation().search)
  return legacy ? <Navigate to={legacy} replace /> : <RuleSetsList />
}

function RuleSetsList() {
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const rulesQuery = useRuleSets(scope)
  const templatesQuery = useTemplates(scope)
  const items = rulesQuery.data?.items ?? NONE
  const templates = templatesQuery.data?.items ?? NONE
  const failed = rulesQuery.isError || templatesQuery.isError
  const [q, setQ] = useUrlState('q', urlCodecs.string())
  const navigate = useNavigate()
  const link = useObjectLink()
  // The page opened carries this list's place (search), so its back link returns to the list the way it was left.
  const open = (to: string) => { const l = link(to); void navigate(l.to, { state: l.state }) }
  // A rule set whose slug is `new` (in any case) cannot have a page: its address is the create page's. It stays in the list, and can be deleted from it.
  const openRuleSet = (rs: RuleSet) => {
    if (isReservedSlug(rs.slug)) { pushToast({ message: t('admin:rules.slug_unreachable', { slug: rs.slug }), severity: 'warning' }); return }
    open(ruleSetPath(rs.slug))
  }
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)

  /** Patch the cached list after a write, so the table updates without a round-trip. */
  function mutateItems(updater: (prev: RuleSet[]) => RuleSet[]) {
    queryClient.setQueryData<ListResponse<RuleSet>>(ruleKeys.list(scope, {}), prev => (prev ? { ...prev, items: updater(prev.items) } : prev))
  }
  // Reload after a write. Clearing the selection is deliberate (the rows it referred to may be gone).
  function load() {
    setChecked({})
    return Promise.all([rulesQuery.refetch(), templatesQuery.refetch()])
  }
  const usedBy = (rs: RuleSet) => templates.filter(tpl => (tpl.rule_sets || []).includes(rs.slug)).map(x => x.name || x.slug)
  const seeded = isSeeded
  const withBusy = async (slug: string, run: () => Promise<void>) => {
    setBusy(prev => new Set(prev).add(slug))
    try { await run() } catch { /* The request layer already surfaced the failure. */ } finally {
      setBusy(prev => { const next = new Set(prev); next.delete(slug); return next })
    }
  }

  async function confirmDelete(rs: RuleSet) {
    if (!await askDeleteRuleSet(t, rs.slug, usedBy(rs))) return
    await withBusy(rs.slug, async () => {
      await deleteRuleSet(rs.slug)
      pushToast({ message: t('admin:rules.toast.deleted'), severity: 'success' })
      await load()
    })
  }

  async function confirmReset(rs: RuleSet) {
    if (!await askResetRuleSet(t, rs.slug)) return
    await withBusy(rs.slug, async () => {
      await resetRuleSet(rs.slug)
      pushToast({ message: t('admin:rules.toast.reset'), severity: 'success' })
      await load()
    })
  }

  function menuOf(rs: RuleSet): MoreMenuItem[] {
    const wait = busy.has(rs.slug)
    const menu: MoreMenuItem[] = [{ id: 'duplicate', label: t('admin:rules.duplicate'), disabled: wait, onSelect: () => open(newRuleSetPath(rs.slug)) }]
    if (seeded(rs)) menu.push({ id: 'reset', label: t('admin:rules.reset_to_default'), disabled: wait, onSelect: () => void confirmReset(rs) })
    // A seeded rule set cannot be deleted; the item stays, off, and says why instead of vanishing.
    menu.push(seeded(rs)
      ? { id: 'delete', label: t('admin:rules.row_delete'), disabledReason: t('admin:rules.cannot_delete_seeded'), icon: IconDelete, destructive: true, disabled: true }
      : { id: 'delete', label: t('admin:rules.row_delete'), icon: IconDelete, destructive: true, disabled: wait, onSelect: () => void confirmDelete(rs) })
    return menu
  }

  async function batchSetEnabled(rows: RuleSet[], enabled: boolean) {
    if (!rows.length) return
    setBatchBusy(true)
    try {
      // Each rule set is saved against the revision the list holds for it, so one that someone saved since is refused, not overwritten.
      // A row without a revision is not sent at all (that would be an unconditional overwrite) and is counted with the stale ones.
      const results = await allSettledLimited(rows, r => (r.revision ? saveRuleSet({ ...r, enabled }, r.revision) : Promise.reject(new MissingRevision())))
      const failures = results.flatMap(r => (r.status === 'rejected' ? [r.reason instanceof MissingRevision ? { kind: 'stale' as const } : ruleSetFailureOf(r.reason)] : []))
      const stale = failures.filter(f => f.kind === 'stale').length
      const fail = failures.length - stale
      if (fail > 0) pushToast({ message: t('admin:rules.toast.batch_partial', { ok: rows.length - fail - stale, fail }), severity: 'warning' })
      else if (stale === 0) pushToast({ message: t(enabled ? 'admin:rules.toast.batch_enabled' : 'admin:rules.toast.batch_disabled', { count: rows.length }), severity: 'success' })
      // A refusal for a stale revision is not retried: the list is reloaded below and the person decides again from what is there.
      if (stale > 0) pushToast({ message: t('admin:rules.toast.batch_stale', { count: stale }), severity: 'warning' })
      await load()
    } finally { setBatchBusy(false) }
  }

  async function batchDelete(rows: RuleSet[], hidden: number, clear: () => void) {
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:rules.confirm.batch_delete_title'),
      message: t('admin:rules.confirm.batch_delete_message', { count: rows.length }) + (hidden > 0 ? ' ' + t('admin:rules.confirm.batch_delete_hidden', { count: hidden }) : ''),
      confirmText: t('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy(true)
    try {
      const results = await allSettledLimited(rows, r => deleteRuleSet(r.slug))
      const okSlugs = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.slug)
      const fail = rows.length - okSlugs.length
      mutateItems(prev => prev.filter(x => !okSlugs.includes(x.slug)))
      clear()
      if (fail > 0) pushToast({ message: t('admin:rules.toast.batch_partial', { ok: okSlugs.length, fail }), severity: 'warning' })
      else pushToast({ message: t('admin:rules.toast.batch_deleted', { count: okSlugs.length }), severity: 'success' })
    } finally { setBatchBusy(false) }
  }

  const columns = useMemo(() => ruleSetColumns({
    t, usedBy, canConfig, busy, onEdit: openRuleSet,
    menuOf,
  // The row handlers close over the lists they read; they are rebuilt with the columns when those change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [t, templates, canConfig, busy, items])

  const shown = useMemo(() => filterRows(items, r => `${r.slug} ${r.name}`, q), [items, q])
  const filtered = q.trim() !== ''

  if (failed) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('admin:rules.title')} description={t('admin:rules.subtitle')} />
        <ErrorState title={t('admin:rules.load_failed')} reason={rulesQuery.error ?? templatesQuery.error} retry={() => load()} />
      </div>
    )
  }

  const loading = rulesQuery.isPending
  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:rules.title')}
        count={loading ? undefined : items.length}
        description={t('admin:rules.subtitle')}
        primary={canConfig && <Button onClick={() => open(newRuleSetPath())}>{t('admin:rules.create')}</Button>}
      />
      <DataTable<RuleSet>
        aria-label={t('admin:rules.title')}
        data={shown}
        columns={columns}
        getRowId={r => r.slug}
        rowLabel={r => r.name || r.slug}
        loading={loading}
        selectable={canConfig}
        selection={checked}
        onSelectionChange={setChecked}
        onRowOpen={openRuleSet}
        rowOptions={r => ({ selectable: !seeded(r), state: busy.has(r.slug) ? 'pending' : r.enabled ? undefined : 'dimmed' })}
        renderCard={(r, ctx) => (
          <RowCard title={r.name || r.slug} subtitle={r.slug} open={ctx.open}
            status={<StatusDot tone={r.enabled ? 'success' : 'neutral'} label={t(r.enabled ? 'admin:rules.status.enabled' : 'admin:rules.status.disabled')} />}
            detail={`${t('admin:rules.table.lines')}: ${countLines(r.content)} · ${usedBy(r).join('、') || t('admin:rules.status.no_usage')}`}
            actions={canConfig ? <RowActions name={r.name || r.slug} editLabel={t('admin:rules.edit_title')} menuLabel={t('admin:rules.row_menu', { name: r.name || r.slug })} busy={busy.has(r.slug)} onEdit={() => openRuleSet(r)} menu={menuOf(r)} /> : undefined} />
        )}
        toolbar={(
          <FilterBar
            search={{ value: q, onChange: setQ, placeholder: t('admin:rules.search_placeholder') }}
            onClearAll={() => setQ('')}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:rules.no_match')} action={<Button variant="outline" onClick={() => setQ('')}>{t('admin:rules.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:rules.empty')} description={t('admin:rules.empty_hint')} action={canConfig ? <Button onClick={() => open(newRuleSetPath())}>{t('admin:rules.create')}</Button> : undefined} />}
        bulkActions={canConfig ? (ctx => {
          const rows = items.filter(i => ctx.ids.includes(i.slug))
          return (
            <>
              <Button variant="outline" size="sm" disabled={batchBusy} onClick={() => void batchSetEnabled(rows, true)}>{t('admin:rules.batch_enable')}</Button>
              <Button variant="outline" size="sm" disabled={batchBusy} onClick={() => void batchSetEnabled(rows, false)}>{t('admin:rules.batch_disable')}</Button>
              <Button variant="outline" size="sm" disabled={batchBusy} className="text-destructive" onClick={() => void batchDelete(rows, ctx.hiddenCount, ctx.clear)}>
                <IconDelete />{t('admin:rules.batch_delete')}
              </Button>
            </>
          )
        }) : undefined}
      />
    </div>
  )
}
