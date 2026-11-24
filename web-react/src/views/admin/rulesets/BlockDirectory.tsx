import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'

import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { Badge } from '@/components/ui/badge'
import { DataTable } from '@/components/data/DataTable'
import type { ReorderCall } from '@/components/data/dataTableTypes'
import { EmptyState } from '@/components/feedback/EmptyState'
import { noteOf, type Block } from '@/utils/ruleset/blocks'
import type { Doc } from '@/utils/ruleset/parse'

/** The height the two scrolling columns share: what is left of the screen under the page header and tabs. */
export const COLUMN_HEIGHT = 'max(24rem, calc(100dvh - 21rem))'

/**
 * The left column (design 3.3): the groups in match order, each with its place, target and rule count. A row opens the
 * group; the drag handle (or the keyboard model the table gives it) reorders; the row menu has the rest. While a search
 * or a target filter is on, only groups with hits are listed and dragging is off, because the report of a reorder is
 * relative to the rows shown.
 */
export function BlockDirectory({ doc, blocks, shown, hits, selected, onSelect, readOnly, onReorder, reorderBlockedReason, menuOf, onAdd }: {
  doc: Doc
  blocks: readonly Block[]
  /** The groups to list (all of them unless something is filtered). */
  shown: readonly Block[]
  /** Search hits per group index, when a search is on. */
  hits: ReadonlyMap<number, number> | null
  selected: number
  onSelect: (index: number) => void
  readOnly: boolean
  onReorder: (call: ReorderCall) => void
  reorderBlockedReason?: string
  menuOf: (block: Block) => MoreMenuItem[]
  onAdd: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const columns = useMemo<ColumnDef<Block, any>[]>(() => {
    const cols: ColumnDef<Block, any>[] = [
      {
        id: 'group', header: t('admin:rules.blocks.title'), enableSorting: false, meta: { hideable: false, label: t('admin:rules.blocks.title'), className: 'max-w-0 overflow-hidden' },
        cell: ({ row }) => {
          const b = row.original
          const note = b.kind === 'rules' ? noteOf(doc, b).split('\n')[0] : ''
          const name = b.kind === 'text' ? t('admin:rules.blocks.text_block', { count: b.count }) : b.target || '—'
          return (
            <div className="flex min-w-0 flex-col" title={note ? `${name}\n${note}` : name}>
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="w-6 shrink-0 text-right text-xs text-muted-foreground tabular-nums">{b.index + 1}</span>
                <span className="truncate font-medium">{name}</span>
                {b.fallback && <Badge variant="outline" className="shrink-0">{t('admin:rules.blocks.fallback')}</Badge>}
              </span>
              {note && <span className="truncate pl-[1.875rem] text-xs text-muted-foreground">{note}</span>}
            </div>
          )
        },
      },
      {
        id: 'count', header: t('admin:rules.matchers.count_col'), enableSorting: false, meta: { label: t('admin:rules.matchers.count_col'), align: 'right', className: 'w-16 tabular-nums' },
        cell: ({ row }) => <span>{hits ? (hits.get(row.original.index) ?? 0).toLocaleString() : row.original.count.toLocaleString()}</span>,
      },
    ]
    if (!readOnly) {
      cols.push({
        id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:rules.table.actions'), className: 'w-12' },
        cell: ({ row }) => (
          <MoreMenu items={menuOf(row.original)} size="icon-xs" label={t('admin:rules.blocks.menu', { index: row.original.index + 1 })} />
        ),
      })
    }
    return cols
    // The menu closes over the document and the other groups; it is rebuilt with them.
  }, [t, doc, hits, readOnly, menuOf])

  if (blocks.length === 0) {
    return (
      <EmptyState compact title={t('admin:rules.blocks.empty_title')} description={t('admin:rules.blocks.empty_body')}
        action={readOnly ? undefined : <button type="button" className="text-sm underline underline-offset-4" onClick={onAdd}>{t('admin:rules.blocks.add')}</button>} />
    )
  }

  const common = {
    'aria-label': t('admin:rules.blocks.title'),
    data: shown,
    columns,
    getRowId: (b: Block) => String(b.index),
    rowLabel: (b: Block) => (b.kind === 'text' ? t('admin:rules.blocks.text_block', { count: b.count }) : b.target),
    onRowOpen: (b: Block) => onSelect(b.index),
    activeRowId: String(selected),
    columnMenu: false,
    maxHeight: COLUMN_HEIGHT,
  }
  return readOnly
    ? <DataTable<Block> {...common} pagination={false} />
    : <DataTable<Block> {...common} pagination={false} onReorder={onReorder} reorderBlockedReason={reorderBlockedReason} />
}
