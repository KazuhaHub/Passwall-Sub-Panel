import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'

import { DataTable } from '@/components/data/DataTable'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { Block } from '@/utils/ruleset/blocks'
import type { Doc } from '@/utils/ruleset/parse'
import { TABLE_HEIGHT } from './BlockDirectory'
import { typeName } from './ruleTypes'

interface Row {
  id: number
  /** Position of the group the rule is in (1-based, as the directory numbers them) and the rule's line in the text. */
  group: number
  line: number
  type: string
  known: boolean
  shown: string
  options: string[]
}

/**
 * "View by target" (design 3.8): every rule that points at one outbound, from all the groups that do, in one list. It is a
 * way to READ, nothing more: the same outbound appears in several places on purpose (the first rule that matches decides),
 * so rules from different places cannot be reordered, moved or deleted from a list that hides where each one sits. The note
 * above the table says so, and a row opens its own group, where editing happens.
 */
export function TargetView({ doc, blocks, target, query, onOpenGroup }: {
  doc: Doc
  blocks: readonly Block[]
  target: string
  /** The global search, narrowing the rows. */
  query: string
  onOpenGroup: (index: number) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const runs = useMemo(() => blocks.filter(b => b.kind === 'rules' && b.target === target), [blocks, target])
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    for (const b of runs) {
      for (let i = b.ruleStart; i < b.end; i++) {
        const r = doc.lines[i].rule!
        out.push({ id: doc.lines[i].id, group: b.index + 1, line: i + 1, type: r.type, known: r.known, options: r.options, shown: r.structured ? r.value : r.fields.slice(1).join(',') })
      }
    }
    return out
  }, [doc, runs])
  const shown = useMemo(() => (query ? rows.filter(r => `${r.type} ${r.shown}`.toLowerCase().includes(query)) : rows), [rows, query])

  const columns = useMemo<ColumnDef<Row, any>[]>(() => [
    { id: 'group', header: t('admin:rules.targetview.col_group'), accessorFn: r => r.group, meta: { hideable: false, label: t('admin:rules.targetview.col_group'), className: 'w-20 tabular-nums' } },
    { id: 'line', header: t('admin:rules.targetview.col_line'), accessorFn: r => r.line, meta: { label: t('admin:rules.targetview.col_line'), className: 'w-20 tabular-nums text-muted-foreground' } },
    {
      id: 'type', header: t('admin:rules.matchers.col_type'), accessorFn: r => r.type.toUpperCase(), meta: { hideable: false, label: t('admin:rules.matchers.col_type'), className: 'w-44 whitespace-nowrap' },
      cell: ({ row }) => <span title={row.original.type}>{typeName(t, row.original.type, row.original.known)}</span>,
    },
    {
      id: 'value', header: t('admin:rules.matchers.col_value'), accessorFn: r => r.shown, meta: { hideable: false, label: t('admin:rules.matchers.col_value'), className: 'max-w-0 truncate font-mono text-xs' },
      cell: ({ row }) => <span title={row.original.shown}>{row.original.shown}</span>,
    },
    {
      id: 'options', header: t('admin:rules.matchers.col_options'), enableSorting: false, meta: { label: t('admin:rules.matchers.col_options'), className: 'w-28' },
      cell: ({ row }) => row.original.options.map(o => <Badge key={o} variant="outline" className="mr-1">{o.toLowerCase() === 'no-resolve' ? t('admin:rules.matchers.no_resolve') : o}</Badge>),
    },
  ], [t])

  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t('admin:rules.targetview.table', { target })}>
      <p className="text-sm text-muted-foreground">{t('admin:rules.targetview.note', { target, count: rows.length.toLocaleString(), blocks: runs.length.toLocaleString() })}</p>
      <DataTable<Row>
        tableId="admin.ruleSets.targets"
        aria-label={t('admin:rules.targetview.table', { target })}
        data={shown}
        columns={columns}
        getRowId={r => String(r.id)}
        rowLabel={r => r.shown}
        pagination={false}
        columnMenu={false}
        maxHeight={TABLE_HEIGHT}
        onRowOpen={r => onOpenGroup(r.group - 1)}
        empty={<EmptyState compact title={t('admin:rules.matchers.no_match')} />}
        renderCard={(r, ctx) => (
          <div className="flex items-start gap-2 p-3">
            <button type="button" className="min-w-0 flex-1 text-left" onClick={ctx.open}>
              <span className="block text-xs text-muted-foreground">{`${r.group} · ${typeName(t, r.type, r.known)}`}</span>
              <span className="block font-mono text-sm break-all">{r.shown}</span>
            </button>
            <Button variant="ghost" size="sm" onClick={ctx.open}>{t('admin:rules.targetview.open', { index: r.group })}</Button>
          </div>
        )}
      />
    </section>
  )
}
