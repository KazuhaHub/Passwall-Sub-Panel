import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'

import type { RuleSet } from '@/api/rules'
import { deleteTemplate, resetTemplate, type Template } from '@/api/templates'
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
import { templateKeys } from '@/query/keys'
import { useRuleSets, useTemplates } from '@/query/rules'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'
import { RowActions } from './subscription/RowActions'
import { RowCard } from './subscription/RowCard'
import { Badge } from '@/components/ui/badge'
import { templateColumns } from './templates/columns'
import { askDeleteTemplate, askResetTemplate, isProtected as protectedRow, isSeeded as seeded } from './templates/actions'
import { legacyTemplateTarget, newTemplatePath, templatePath } from './templates/templateRoutes'

const NONE: never[] = []

/**
 * Templates: a client format plus the rule sets bound to it; subscription rendering uses the bindings set here.
 * The list is here; a template is NOT opened here: the row, the edit button and "New template" go to the template's own
 * page (templates/TemplateRoute). `?edit=<slug>` / `?edit=new[&copy=<slug>]` are the addresses the old side sheet used;
 * they are sent on to the new routes (see `legacyTemplateTarget`) because bookmarks still carry them.
 */
export default function TemplatesView() {
  const legacy = legacyTemplateTarget(useLocation().search)
  return legacy ? <Navigate to={legacy} replace /> : <TemplatesList />
}

function TemplatesList() {
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const templatesQuery = useTemplates(scope)
  const ruleSetsQuery = useRuleSets(scope)
  const items = templatesQuery.data?.items ?? NONE
  const ruleSets = ruleSetsQuery.data?.items ?? NONE
  const failed = templatesQuery.isError || ruleSetsQuery.isError
  const [q, setQ] = useUrlState('q', urlCodecs.string())
  const navigate = useNavigate()
  const link = useObjectLink()
  // The page opened carries this list's place (search), so its back link returns to the list the way it was left.
  const open = (to: string) => { const l = link(to); void navigate(l.to, { state: l.state }) }
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const ruleSetMap = useMemo(() => new Map<string, RuleSet>(ruleSets.map(r => [r.slug, r])), [ruleSets])

  /** Patch the cached list after a write, so the table updates without a round-trip. */
  function mutateItems(updater: (prev: Template[]) => Template[]) {
    queryClient.setQueryData<ListResponse<Template>>(templateKeys.list(scope, {}), prev => (prev ? { ...prev, items: updater(prev.items) } : prev))
  }
  // Reload after a write. Clearing the selection is deliberate (the rows it referred to may be gone).
  function load() {
    setChecked({})
    return Promise.all([templatesQuery.refetch(), ruleSetsQuery.refetch()])
  }
  const withBusy = async (slug: string, run: () => Promise<void>) => {
    setBusy(prev => new Set(prev).add(slug))
    try { await run() } catch { /* The request layer already surfaced the failure. */ } finally {
      setBusy(prev => { const next = new Set(prev); next.delete(slug); return next })
    }
  }

  async function confirmDelete(tpl: Template) {
    if (!await askDeleteTemplate(t, tpl.slug)) return
    await withBusy(tpl.slug, async () => {
      await deleteTemplate(tpl.slug)
      pushToast({ message: t('admin:templates.toast.deleted'), severity: 'success' })
      await load()
    })
  }

  async function confirmReset(tpl: Template) {
    if (!await askResetTemplate(t, tpl.slug)) return
    await withBusy(tpl.slug, async () => {
      await resetTemplate(tpl.slug)
      pushToast({ message: t('admin:templates.toast.reset'), severity: 'success' })
      await load()
    })
  }

  function menuOf(tpl: Template): MoreMenuItem[] {
    const wait = busy.has(tpl.slug)
    const menu: MoreMenuItem[] = [{ id: 'duplicate', label: t('admin:templates.duplicate'), disabled: wait, onSelect: () => open(newTemplatePath(tpl.slug)) }]
    if (seeded(tpl)) menu.push({ id: 'reset', label: t('admin:templates.reset_to_default'), disabled: wait, onSelect: () => void confirmReset(tpl) })
    // A protected template keeps its Delete item, off, saying why, instead of silently having none.
    menu.push(protectedRow(tpl)
      ? { id: 'delete', label: t('admin:templates.row_delete'), disabledReason: t(seeded(tpl) ? 'admin:templates.cannot_delete_seeded' : 'admin:templates.cannot_delete_default'), icon: IconDelete, destructive: true, disabled: true }
      : { id: 'delete', label: t('admin:templates.row_delete'), icon: IconDelete, destructive: true, disabled: wait, onSelect: () => void confirmDelete(tpl) })
    return menu
  }

  async function batchDelete(rows: Template[], hidden: number, clear: () => void) {
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:templates.confirm.batch_delete_title'),
      message: t('admin:templates.confirm.batch_delete_message', { count: rows.length }) + (hidden > 0 ? ' ' + t('admin:templates.confirm.batch_delete_hidden', { count: hidden }) : ''),
      confirmText: t('common:actions.delete'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy(true)
    try {
      const results = await allSettledLimited(rows, r => deleteTemplate(r.slug))
      const okSlugs = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.slug)
      const fail = rows.length - okSlugs.length
      mutateItems(prev => prev.filter(x => !okSlugs.includes(x.slug)))
      clear()
      if (fail > 0) pushToast({ message: t('admin:templates.toast.batch_partial', { ok: okSlugs.length, fail }), severity: 'warning' })
      else pushToast({ message: t('admin:templates.toast.batch_deleted', { count: okSlugs.length }), severity: 'success' })
    } finally { setBatchBusy(false) }
  }

  const columns = useMemo(() => templateColumns({
    t, ruleSets: ruleSetMap, canConfig, busy, onEdit: tpl => open(templatePath(tpl.slug)), menuOf,
  // The row handlers close over the lists they read; they are rebuilt with the columns when those change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [t, ruleSetMap, canConfig, busy, items])

  const shown = useMemo(() => filterRows(items, r => `${r.slug} ${r.name} ${r.client_type}`, q), [items, q])
  const filtered = q.trim() !== ''

  if (failed) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('admin:templates.title')} description={t('admin:templates.subtitle')} />
        <ErrorState title={t('admin:templates.load_failed')} reason={templatesQuery.error ?? ruleSetsQuery.error} retry={() => load()} />
      </div>
    )
  }

  const loading = templatesQuery.isPending
  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('admin:templates.title')}
        count={loading ? undefined : items.length}
        description={t('admin:templates.subtitle')}
        primary={canConfig && <Button onClick={() => open(newTemplatePath())}>{t('admin:templates.create')}</Button>}
      />
      <DataTable<Template>
        aria-label={t('admin:templates.title')}
        data={shown}
        columns={columns}
        getRowId={r => r.slug}
        rowLabel={r => r.name || r.slug}
        loading={loading}
        selectable={canConfig}
        selection={checked}
        onSelectionChange={setChecked}
        onRowOpen={canConfig ? tpl => open(templatePath(tpl.slug)) : undefined}
        rowOptions={r => ({ selectable: !protectedRow(r), state: busy.has(r.slug) ? 'pending' : undefined })}
        renderCard={(r, ctx) => (
          <RowCard title={r.name || r.slug} subtitle={r.slug} open={ctx.open}
            status={r.is_default ? <Badge variant="secondary">{t('admin:templates.status.default')}</Badge> : undefined}
            detail={`${r.client_type} · ${r.rule_sets?.length ? r.rule_sets.map(s => ruleSetMap.get(s)?.name || s).join('、') : t('admin:templates.status.no_binding')}`}
            actions={canConfig ? <RowActions name={r.name || r.slug} editLabel={t('admin:templates.edit_title')} menuLabel={t('admin:templates.row_menu', { name: r.name || r.slug })} busy={busy.has(r.slug)} onEdit={() => open(templatePath(r.slug))} menu={menuOf(r)} /> : undefined} />
        )}
        toolbar={(
          <FilterBar
            search={{ value: q, onChange: setQ, placeholder: t('admin:templates.search_placeholder') }}
            onClearAll={() => setQ('')}
          />
        )}
        empty={filtered
          ? <EmptyState compact title={t('admin:templates.no_match')} action={<Button variant="outline" onClick={() => setQ('')}>{t('admin:templates.clear_search')}</Button>} />
          : <EmptyState compact title={t('admin:templates.empty')} description={t('admin:templates.empty_hint')} action={canConfig ? <Button onClick={() => open(newTemplatePath())}>{t('admin:templates.create')}</Button> : undefined} />}
        bulkActions={canConfig ? (ctx => {
          const rows = items.filter(i => ctx.ids.includes(i.slug))
          return (
            <Button variant="outline" size="sm" disabled={batchBusy} className="text-destructive" onClick={() => void batchDelete(rows, ctx.hiddenCount, ctx.clear)}>
              <IconDelete />{t('admin:templates.batch_delete')}
            </Button>
          )
        }) : undefined}
      />
    </div>
  )
}
