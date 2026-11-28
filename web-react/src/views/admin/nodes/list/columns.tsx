import type { TFunction } from 'i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import type { LandingColor } from '@/components/diagram/landingColor'
import { Link } from 'react-router'
import { formatDualTz } from '@/utils/datetime'
import { regionLabel } from '@/utils/regions'
import i18n from '@/i18n'
import type { PathLabels } from '../shared/pathOf'
import { statusOf } from '../shared/statusOf'
import { AddressCell, FactorCell, NameCell, PathCell, RowMenu, StatusCell, SwitchCell, TagsCell } from './cells'
import type { ListRow, TreeItem } from './types'

export interface ColumnCtx {
  t: TFunction
  colors: Map<string, LandingColor>
  labels: PathLabels
  tz: string
  /** An administrator's link to a node; undefined for an operator, who has no detail page. */
  nodeHref?: (nodeId: number) => string
  /** `parent` is a landing's parent row in the by-node view, whose menu also offers Add line (the landing is what it adds to). */
  /** Some visible row has a traffic multiplier: only then is the column drawn. */
  hasFactor?: boolean
  menuOf: (item: ListRow, opts?: { parent?: boolean }) => MoreMenuItem[]
}

// The default columns share the frame in percentages (max-w-0 lets a cell shrink below its content) and clip with an
// ellipsis, so the handle, checkbox and menu columns can never be pushed out of view; the full text is in `title`.
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

const sortless = { enableSorting: false } as const

/**
 * The order view's columns (design 2.2). Defined for ONE stable array (the caller memoises on ctx), because
 * DataTable memoises its rows on the columns' identity. Sorting is off throughout: the order IS the content, and
 * a sorted order list could not be dragged.
 */
export function orderColumns(c: ColumnCtx): ColumnDef<ListRow, any>[] {
  const { t } = c
  return [
    { id: 'line', header: t('nodes.col.line'), ...sortless, meta: { hideable: false, label: t('nodes.col.line'), className: FIT + ' w-[16%]' },
      cell: ctx => <NameCell item={ctx.row.original} colors={c.colors} /> },
    { id: 'path', header: t('nodes.col.path'), ...sortless, meta: { label: t('nodes.col.path'), className: FIT + ' w-[38%]' },
      cell: ctx => <PathCell item={ctx.row.original} colors={c.colors} labels={c.labels} nodeHref={c.nodeHref?.(ctx.row.original.row.node_id ?? 0)} /> },
    { id: 'address', header: t('nodes.col.address'), ...sortless, meta: { label: t('nodes.col.address'), className: FIT + ' w-[22%]' },
      cell: ctx => <AddressCell row={ctx.row.original.row} /> },
    // A narrow fixed column, and absent altogether while no row on the list has a multiplier (it is empty on most lists).
    ...(c.hasFactor ? [{ id: 'factor', header: t('nodes.col.factor'), ...sortless, meta: { label: t('nodes.col.factor'), align: 'right' as const, className: 'w-14' },
      cell: (ctx: { row: { original: ListRow } }) => <FactorCell row={ctx.row.original.row} /> }] : []),
    // No ellipsis here: a status says why in words, and a clipped word ("Direct line hidde") is worse than a wider column.
    { id: 'status', header: t('nodes.col.status'), ...sortless, meta: { label: t('nodes.col.status'), className: 'w-[18%] min-w-[11.5rem]' },
      cell: ctx => <StatusCell item={ctx.row.original} /> },
    { id: 'landing_server', header: t('nodes.col.landing_server'), ...sortless, meta: { label: t('nodes.col.landing_server'), defaultHidden: true },
      cell: ctx => ctx.row.original.row.landing?.panel_name ?? '' },
    { id: 'region', header: t('nodes.col.region'), ...sortless, meta: { label: t('nodes.col.region'), defaultHidden: true },
      cell: ctx => regionLabel(ctx.row.original.row.landing?.region ?? '', i18n.resolvedLanguage ?? 'en') },
    { id: 'tags', header: t('nodes.col.tags'), ...sortless, meta: { label: t('nodes.col.tags'), defaultHidden: true },
      cell: ctx => <TagsCell item={ctx.row.original} /> },
    { id: 'id', header: t('nodes.col.id'), ...sortless, meta: { label: t('nodes.col.id'), defaultHidden: true, className: 'font-mono text-muted-foreground' },
      cell: ctx => String(ctx.row.original.row.node_id ?? '') },
    { id: 'protocol', header: t('nodes.col.protocol'), ...sortless, meta: { label: t('nodes.col.protocol'), defaultHidden: true },
      cell: ctx => ctx.row.original.node?.desired_protocol ?? ctx.row.original.node?.protocol ?? '—' },
    { id: 'checked_at', header: t('nodes.col.checked_at'), ...sortless, meta: { label: t('nodes.col.checked_at'), defaultHidden: true },
      cell: ctx => formatDualTz(ctx.row.original.row.health?.checked_at, c.tz) },
    { id: 'enabled', header: t('nodes.col.enabled'), ...sortless, meta: { label: t('nodes.col.enabled'), align: 'right', className: 'w-16' },
      cell: ctx => <SwitchCell item={ctx.row.original} items={c.menuOf(ctx.row.original)} /> },
    { id: 'actions', header: '', ...sortless, meta: { hideable: false, align: 'right', label: t('nodes.col.actions'), className: 'w-10' },
      cell: ctx => <RowMenu items={c.menuOf(ctx.row.original)} name={ctx.row.original.row.name} /> },
  ]
}

/** The by-node view: a landing is a parent row (its direct line), its relay lines are its children. */
export function treeColumns(c: ColumnCtx): ColumnDef<TreeItem, any>[] {
  const { t } = c
  return [
    { id: 'line', header: t('nodes.col.line'), ...sortless, meta: { hideable: false, label: t('nodes.col.line'), className: 'min-w-48 max-w-72' },
      cell: ctx => {
        const item = ctx.row.original
        if (item.child && item.list) return <NameCell item={item.list} colors={c.colors} />
        const href = c.nodeHref?.(item.group.node_id)
        const name = href ? <Link to={href} className="truncate font-medium hover:underline" title={t('nodes.action.open_node')}>{item.group.name}</Link> : <span className="truncate font-medium">{item.group.name}</span>
        return (
          <div className="flex min-w-0 items-center gap-2">
            <LandingColorBar color={c.colors.get(String(item.group.node_id))} className="h-5 self-center" />
            {name}
            <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{t('nodes.list.lines_of_node', { count: (item.list ? 1 : 0) + item.group.children.length })}</span>
          </div>
        )
      } },
    { id: 'path', header: t('nodes.col.path'), ...sortless, meta: { label: t('nodes.col.path'), className: 'min-w-56' },
      cell: ctx => ctx.row.original.list && <PathCell item={ctx.row.original.list} colors={c.colors} labels={c.labels} /> },
    { id: 'address', header: t('nodes.col.address'), ...sortless, meta: { label: t('nodes.col.address'), className: 'max-w-56' },
      cell: ctx => ctx.row.original.list && <AddressCell row={ctx.row.original.list.row} /> },
    { id: 'status', header: t('nodes.col.status'), ...sortless, meta: { label: t('nodes.col.status') },
      cell: ctx => ctx.row.original.list && <StatusCell item={ctx.row.original.list} /> },
    { id: 'actions', header: '', ...sortless, meta: { hideable: false, align: 'right', label: t('nodes.col.actions') },
      cell: ctx => ctx.row.original.list && <RowMenu items={c.menuOf(ctx.row.original.list, { parent: true })} name={ctx.row.original.list.row.name} /> },
  ]
}

/** Used by the row's `data-state` hints: a row to dim is a plainly disabled one; one to flag is waiting on its entry. */
export function rowStateOf(item: ListRow): 'dimmed' | 'pending' | undefined {
  const s = statusOf(item.row, item.node)
  if (s.id === 'waiting_entry') return 'pending'
  return s.id === 'landing_disabled' || s.id === 'hidden_direct' || s.id === 'disabled' ? 'dimmed' : undefined
}
