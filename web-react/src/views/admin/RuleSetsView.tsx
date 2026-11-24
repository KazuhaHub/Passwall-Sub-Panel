import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'

import { deleteRuleSet, resetRuleSet, saveRuleSet, SEEDED_RULESET_SLUGS, type RuleSet } from '@/api/rules'
import type { ListResponse } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { DataTable } from '@/components/data/DataTable'
import { FilterBar } from '@/components/data/FilterBar'
import { filterRows } from '@/components/data/dataTableLogic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconDelete } from '@/lib/icons'
import { useAllGroups } from '@/query/groups'
import { ruleKeys } from '@/query/keys'
import { useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'
import { ruleSetColumns } from './rulesets/columns'
import { countLines, duplicateOf } from './rulesets/model'
import { RowActions } from './subscription/RowActions'
import { RowCard } from './subscription/RowCard'
import { StatusDot } from '@/components/common/StatusDot'
import { RuleSetSheet, type SheetTarget } from './rulesets/RuleSetSheet'

const NONE: never[] = []

/**
 * Rule sets: reusable snippets of routing rules that only take part in a subscription when a template binds them.
 * The list is here; one is edited in a wide sheet (rulesets/RuleSetSheet), opened from the address (`?edit=<slug>`,
 * `?edit=new`, or `?edit=new&copy=<slug>` for a duplicate) so it can be linked, reloaded and closed with Back.
 */
export default function RuleSetsView() {
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const rulesQuery = useRuleSets(scope)
  const templatesQuery = useTemplates(scope)
  const groupsQuery = useAllGroups(scope)
  const items = rulesQuery.data?.items ?? NONE
  const templates = templatesQuery.data?.items ?? NONE
  const groups = groupsQuery.data ?? NONE
  const failed = rulesQuery.isError || templatesQuery.isError || groupsQuery.isError
  const [q, setQ] = useUrlState('q', urlCodecs.string())
  const edit = useUrlSelection('edit')
  const copy = useUrlSelection('copy')
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
    return Promise.all([rulesQuery.refetch(), templatesQuery.refetch(), groupsQuery.refetch()])
  }
  const usedBy = (rs: RuleSet) => templates.filter(tpl => (tpl.rule_sets || []).includes(rs.slug)).map(x => x.name || x.slug)
  const seeded = (rs: RuleSet) => SEEDED_RULESET_SLUGS.includes(rs.slug)
  const withBusy = async (slug: string, run: () => Promise<void>) => {
    setBusy(prev => new Set(prev).add(slug))
    try { await run() } catch { /* The request layer already surfaced the failure. */ } finally {
      setBusy(prev => { const next = new Set(prev); next.delete(slug); return next })
    }
  }

  async function confirmDelete(rs: RuleSet) {
    const used = usedBy(rs)
    const usage = used.length > 0 ? t('admin:rules.confirm.delete_used_suffix', { names: used.join('、') }) : ''
    const ok = await confirm({
      title: t('admin:rules.confirm.delete_title'),
      message: t('admin:rules.confirm.delete_message', { slug: rs.slug, usage }),
      confirmText: t('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    await withBusy(rs.slug, async () => {
      await deleteRuleSet(rs.slug)
      pushToast({ message: t('admin:rules.toast.deleted'), severity: 'success' })
      await load()
    })
  }

  async function confirmReset(rs: RuleSet) {
    const ok = await confirm({
      title: t('admin:rules.confirm.reset_title'),
      message: t('admin:rules.confirm.reset_message', { slug: rs.slug }),
      confirmText: t('admin:rules.reset_to_default'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    await withBusy(rs.slug, async () => {
      await resetRuleSet(rs.slug)
      pushToast({ message: t('admin:rules.toast.reset'), severity: 'success' })
      await load()
    })
  }

  function menuOf(rs: RuleSet): MoreMenuItem[] {
    const wait = busy.has(rs.slug)
    const menu: MoreMenuItem[] = [{ id: 'duplicate', label: t('admin:rules.duplicate'), disabled: wait, onSelect: () => { copy.select(rs.slug); edit.select('new') } }]
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
      const results = await allSettledLimited(rows, r => saveRuleSet({ ...r, enabled }))
      const fail = results.filter(r => r.status === 'rejected').length
      if (fail > 0) pushToast({ message: t('admin:rules.toast.batch_partial', { ok: rows.length - fail, fail }), severity: 'warning' })
      else pushToast({ message: t(enabled ? 'admin:rules.toast.batch_enabled' : 'admin:rules.toast.batch_disabled', { count: rows.length }), severity: 'success' })
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
    t, usedBy, canConfig, busy, onEdit: rs => edit.select(rs.slug),
    menuOf,
  // The row handlers close over the lists they read; they are rebuilt with the columns when those change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [t, templates, canConfig, busy, items])

  const shown = useMemo(() => filterRows(items, r => `${r.slug} ${r.name}`, q), [items, q])
  const filtered = q.trim() !== ''

  // What the sheet shows, from the address: an existing slug, `new`, or `new` with `copy=<slug>`.
  const target: SheetTarget | null = useMemo(() => {
    if (!canConfig || !edit.selected) return null
    if (edit.selected === 'new') {
      const source = copy.selected ? items.find(r => r.slug === copy.selected) : undefined
      return source ? { kind: 'copy', draft: duplicateOf(source), source } : { kind: 'new' }
    }
    const found = items.find(r => r.slug === edit.selected)
    return found ? { kind: 'edit', ruleSet: found } : null
  }, [canConfig, edit.selected, copy.selected, items])

  const closeSheet = () => { copy.clear(); edit.clear() }

  if (failed) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('admin:rules.title')} description={t('admin:rules.subtitle')} />
        <ErrorState title={t('admin:rules.load_failed')} reason={rulesQuery.error ?? templatesQuery.error ?? groupsQuery.error} retry={() => load()} />
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
        primary={canConfig && <Button onClick={() => { copy.clear(); edit.select('new') }}>{t('admin:rules.create')}</Button>}
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
        onRowOpen={canConfig ? rs => edit.select(rs.slug) : undefined}
        activeRowId={edit.selected && edit.selected !== 'new' ? edit.selected : null}
        rowOptions={r => ({ selectable: !seeded(r), state: busy.has(r.slug) ? 'pending' : r.enabled ? undefined : 'dimmed' })}
        renderCard={(r, ctx) => (
          <RowCard title={r.name || r.slug} subtitle={r.slug} open={ctx.open}
            status={<StatusDot tone={r.enabled ? 'success' : 'neutral'} label={t(r.enabled ? 'admin:rules.status.enabled' : 'admin:rules.status.disabled')} />}
            detail={`${t('admin:rules.table.lines')}: ${countLines(r.content)} · ${usedBy(r).join('、') || t('admin:rules.status.no_usage')}`}
            actions={canConfig ? <RowActions name={r.name || r.slug} editLabel={t('admin:rules.edit_title')} menuLabel={t('admin:rules.row_menu', { name: r.name || r.slug })} busy={busy.has(r.slug)} onEdit={() => edit.select(r.slug)} menu={menuOf(r)} /> : undefined} />
        )}
        toolbar={(
          <FilterBar
            search={{ value: q, onChange: setQ, placeholder: t('admin:rules.search_placeholder') }}
            onClearAll={() => setQ('')}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:rules.no_match')} action={<Button variant="outline" onClick={() => setQ('')}>{t('admin:rules.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:rules.empty')} description={t('admin:rules.empty_hint')} action={canConfig ? <Button onClick={() => edit.select('new')}>{t('admin:rules.create')}</Button> : undefined} />}
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
      <RuleSetSheet
        target={target}
        existingSlugs={items.map(r => r.slug)}
        groups={groups}
        onClose={closeSheet}
        onSaved={(saved, wasEdit) => {
          if (wasEdit) mutateItems(prev => prev.map(item => (item.slug === saved.slug ? saved : item)))
          closeSheet()
          if (!wasEdit) void load().catch(() => {})
        }}
      />
    </div>
  )
}
