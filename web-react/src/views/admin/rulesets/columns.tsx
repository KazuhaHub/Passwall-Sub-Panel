import type { TFunction } from 'i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { RuleSetSummary } from '@/api/rules'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { StatusDot } from '@/components/common/StatusDot'
import { RowActions } from '../subscription/RowActions'

export interface RuleSetColumnCtx {
  t: TFunction
  /** Names of the templates a rule set is bound to. */
  usedBy: (rs: RuleSetSummary) => string[]
  /** Admin write access: without it a row has no actions. */
  canConfig: boolean
  busy: ReadonlySet<string>
  onEdit: (rs: RuleSetSummary) => void
  menuOf: (rs: RuleSetSummary) => MoreMenuItem[]
}

// Percentage widths with max-w-0 so a long name truncates inside its column instead of widening the table.
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

/** One stable array per ctx (DataTable memoises its rows on the columns' identity). */
export function ruleSetColumns(c: RuleSetColumnCtx): ColumnDef<RuleSetSummary, any>[] {
  const { t } = c
  const cols: ColumnDef<RuleSetSummary, any>[] = [
    { id: 'slug', header: t('admin:rules.table.slug'), accessorFn: r => r.slug, meta: { hideable: false, label: t('admin:rules.table.slug'), className: `${FIT} w-[20%] font-mono text-xs` },
      cell: ctx => <span title={ctx.row.original.slug}>{ctx.row.original.slug}</span> },
    { id: 'name', header: t('admin:rules.table.name'), accessorFn: r => r.name, meta: { hideable: false, label: t('admin:rules.table.name'), className: `${FIT} w-[24%] font-medium` },
      cell: ctx => <span title={ctx.row.original.name}>{ctx.row.original.name}</span> },
    { id: 'sort', header: t('admin:rules.table.sort'), accessorFn: r => r.sort, meta: { label: t('admin:rules.table.sort'), align: 'right', className: 'w-16 tabular-nums' } },
    { id: 'lines', header: t('admin:rules.table.lines'), accessorFn: r => r.line_count, meta: { label: t('admin:rules.table.lines'), align: 'right', className: 'w-20 tabular-nums' } },
    { id: 'used_by', header: t('admin:rules.table.used_by'), enableSorting: false, meta: { label: t('admin:rules.table.used_by'), className: `${FIT} w-[28%]` },
      cell: ctx => {
        const names = c.usedBy(ctx.row.original)
        const text = names.length === 0 ? t('admin:rules.status.no_usage') : names.join('、')
        return <span title={text} className={names.length === 0 ? 'text-muted-foreground' : undefined}>{text}</span>
      } },
    { id: 'status', header: t('admin:rules.table.status'), accessorFn: r => (r.enabled ? 1 : 0), meta: { label: t('admin:rules.table.status'), className: 'w-28' },
      cell: ctx => ctx.row.original.enabled
        ? <StatusDot tone="success" label={t('admin:rules.status.enabled')} />
        : <StatusDot tone="neutral" label={t('admin:rules.status.disabled')} /> },
  ]
  if (c.canConfig) {
    cols.push({ id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:rules.table.actions'), className: 'w-20' },
      cell: ctx => (
        <RowActions name={ctx.row.original.name || ctx.row.original.slug} editLabel={t('admin:rules.edit_title')} menuLabel={t('admin:rules.row_menu', { name: ctx.row.original.name || ctx.row.original.slug })}
          busy={c.busy.has(ctx.row.original.slug)} onEdit={() => c.onEdit(ctx.row.original)} menu={c.menuOf(ctx.row.original)} />
      ) })
  }
  return cols
}
