import type { TFunction } from 'i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { RuleSet } from '@/api/rules'
import type { Template } from '@/api/templates'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import { Badge } from '@/components/ui/badge'
import { RowActions } from '../subscription/RowActions'
import { coverageOf } from './analysis'

export interface TemplateColumnCtx {
  t: TFunction
  ruleSets: ReadonlyMap<string, RuleSet>
  canConfig: boolean
  busy: ReadonlySet<string>
  onEdit: (tpl: Template) => void
  menuOf: (tpl: Template) => MoreMenuItem[]
}

const FIT = 'max-w-0 overflow-hidden text-ellipsis'

/** The "Targets" cell: quiet when the template covers what its rule sets need, words and a badge when it does not. */
function Coverage({ tpl, c }: { tpl: Template; c: TemplateColumnCtx }) {
  const cov = coverageOf(tpl, c.ruleSets)
  if (cov.kind === 'none') return <StatusDot tone="neutral" label={c.t('admin:templates.status.no_binding')} />
  if (cov.kind === 'auto') return <StatusDot tone="success" label={c.t('admin:templates.status.auto_generated')} />
  if (cov.kind === 'ok') return <StatusDot tone="success" label={c.t('admin:templates.status.ok')} />
  return <StatusBadge tone="danger" label={c.t('admin:templates.status.missing_n', { n: cov.names.length })} />
}

/** One stable array per ctx (DataTable memoises its rows on the columns' identity). */
export function templateColumns(c: TemplateColumnCtx): ColumnDef<Template, any>[] {
  const { t } = c
  const summary = (tpl: Template) => (!tpl.rule_sets?.length ? t('admin:templates.status.no_binding') : tpl.rule_sets.map(s => c.ruleSets.get(s)?.name || s).join('、'))
  const cols: ColumnDef<Template, any>[] = [
    { id: 'slug', header: t('admin:templates.table.slug'), accessorFn: r => r.slug, meta: { hideable: false, label: t('admin:templates.table.slug'), className: `${FIT} w-[20%] font-mono text-xs` },
      cell: ctx => <span title={ctx.row.original.slug}>{ctx.row.original.slug}</span> },
    { id: 'name', header: t('admin:templates.table.name'), accessorFn: r => r.name, meta: { hideable: false, label: t('admin:templates.table.name'), className: `${FIT} w-[22%] font-medium` },
      cell: ctx => <span title={ctx.row.original.name}>{ctx.row.original.name}</span> },
    { id: 'client_type', header: t('admin:templates.table.client_type'), accessorFn: r => r.client_type, meta: { label: t('admin:templates.table.client_type'), className: 'w-24 text-muted-foreground' } },
    { id: 'rule_sets', header: t('admin:templates.table.rule_sets'), enableSorting: false, meta: { label: t('admin:templates.table.rule_sets'), className: `${FIT} w-[26%]` },
      cell: ctx => <span title={summary(ctx.row.original)}>{summary(ctx.row.original)}</span> },
    { id: 'coverage', header: t('admin:templates.table.coverage'), enableSorting: false, meta: { label: t('admin:templates.table.coverage'), className: 'w-32' },
      cell: ctx => <Coverage tpl={ctx.row.original} c={c} /> },
    { id: 'default', header: t('admin:templates.table.default'), accessorFn: r => (r.is_default ? 1 : 0), meta: { label: t('admin:templates.table.default'), className: 'w-24' },
      cell: ctx => (ctx.row.original.is_default ? <Badge variant="secondary">{t('admin:templates.status.default')}</Badge> : null) },
  ]
  if (c.canConfig) {
    cols.push({ id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:templates.table.actions'), className: 'w-20' },
      cell: ctx => (
        <RowActions name={ctx.row.original.name || ctx.row.original.slug} editLabel={t('admin:templates.edit_title')} menuLabel={t('admin:templates.row_menu', { name: ctx.row.original.name || ctx.row.original.slug })}
          busy={c.busy.has(ctx.row.original.slug)} onEdit={() => c.onEdit(ctx.row.original)} menu={c.menuOf(ctx.row.original)} />
      ) })
  }
  return cols
}
