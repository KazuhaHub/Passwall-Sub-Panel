import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'

import { IconButton } from '@/components/common/IconButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { DataTable } from '@/components/data/DataTable'
import type { DataTableHandle } from '@/components/data/dataTableTypes'
import { FilterBar } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { IconAdd, IconArrowLeft, IconDelete, IconEdit, IconFile, IconUpload } from '@/lib/icons'
import { noteOf, type Block } from '@/utils/ruleset/blocks'
import type { Doc } from '@/utils/ruleset/parse'
import { TABLE_HEIGHT } from './BlockDirectory'
import { typeName } from './ruleTypes'
import { TargetSelect } from './TargetSelect'
import type { TargetChoices } from './targets'

interface Row {
  /** The line's id (not its index: that changes with every edit above it). */
  id: number
  type: string
  /** What the match column shows: the value, or for a rule the form cannot edit everything after its type. */
  shown: string
  options: string[]
  known: boolean
  structured: boolean
}

/**
 * The middle column (design 3.4): one group. Its heading, its note, where it sends traffic (changing that rewrites
 * every rule of the group, and the button says how many), and its rules as a table. The table is the shared DataTable,
 * which draws only the rows near the screen once a group is long (the seeded set has one of 6,904).
 */
export function BlockDetail({ doc, block, choices, readOnly, query, phone, onBack, focusRow, onEditRow, onAddRow, onPaste, onImport, onDeleteRows, onMoveRows, onRetarget, onSetNote, onEditText }: {
  doc: Doc
  block: Block
  choices: TargetChoices
  readOnly: boolean
  /** The global search, narrowing the rows shown. */
  query: string
  phone: boolean
  onBack: () => void
  /** A line to bring into view and mark (a jump from the routing test or the problems panel); `token` makes the same line count again. */
  focusRow?: { id: number; token: number } | null
  onEditRow: (id: number) => void
  onAddRow: () => void
  onPaste: () => void
  onImport: () => void
  onDeleteRows: (ids: number[]) => void
  onMoveRows: (ids: number[]) => void
  onRetarget: (target: string) => void
  onSetNote: (text: string) => void
  onEditText: (ids: number[]) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [local, setLocal] = useState('')
  const [types, setTypes] = useState<string[]>([])
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  // The target the picker holds now (null: none, or a new name that is not usable).
  const [pending, setPending] = useState<string | null>(null)
  const [noteDraft, setNoteDraft] = useState<string | null>(null)
  const table = useRef<DataTableHandle>(null)
  const [marked, setMarked] = useState<string | null>(null)
  const note = block.kind !== 'text' ? noteOf(doc, block) : ''
  // A run of lines with no target is not a group traffic can be sent to: it is shown and can be edited or deleted, but nothing is added to it, pasted into it or moved out of it.
  const noTarget = block.kind === 'notarget'

  const rows = useMemo<Row[]>(() => {
    if (block.kind === 'text') return []
    const out: Row[] = []
    for (let i = block.ruleStart; i < block.end; i++) {
      const r = doc.lines[i].rule!
      out.push({
        id: doc.lines[i].id, type: r.type, options: r.options, known: r.known, structured: r.structured,
        shown: r.structured ? r.value : r.fields.slice(1).join(','),
      })
    }
    return out
  }, [doc, block])

  // A selection names lines by id; the ones an edit removed are dropped from it.
  useEffect(() => {
    setChecked(prev => {
      const live = new Set(rows.map(r => String(r.id)))
      const keep = Object.fromEntries(Object.entries(prev).filter(([id, on]) => on && live.has(id)))
      return Object.keys(keep).length === Object.keys(prev).length ? prev : keep
    })
  }, [rows])

  // A jump clears what would hide the line, then scrolls to it once the table has drawn.
  useEffect(() => {
    if (!focusRow) return
    setLocal(''); setTypes([])
    setMarked(String(focusRow.id))
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => table.current?.scrollToRow(String(focusRow.id), { align: 'center' })))
    return () => cancelAnimationFrame(frame)
  }, [focusRow])

  const needle = [query, local].map(s => s.trim().toLowerCase()).filter(Boolean)
  const shown = useMemo(() => rows.filter(r => {
    if (types.length > 0 && !types.includes(r.type.toUpperCase())) return false
    const hay = `${r.type} ${r.shown}`.toLowerCase()
    return needle.every(n => hay.includes(n))
    // `needle` is rebuilt from the two strings below each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [rows, types, query, local])

  const typeOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const r of rows) counts.set(r.type.toUpperCase(), (counts.get(r.type.toUpperCase()) ?? 0) + 1)
    return [...counts].sort((a, b) => b[1] - a[1]).map(([value, count]) => ({ value, label: typeName(t, value, true), count }))
  }, [rows, t])

  const rowMenu = (r: Row): MoreMenuItem[] => [
    ...(noTarget ? [] : [{ id: 'move', label: t('admin:rules.matchers.move_to'), onSelect: () => onMoveRows([r.id]) }]),
    { id: 'delete', label: t('admin:rules.matchers.delete_named', { value: r.shown }), icon: IconDelete, destructive: true, onSelect: () => onDeleteRows([r.id]) },
  ]

  const columns = useMemo<ColumnDef<Row, any>[]>(() => {
    const cols: ColumnDef<Row, any>[] = [
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
        cell: ({ row }) => row.original.options.map(o => (
          <Badge key={o} variant="outline" className="mr-1">{o.toLowerCase() === 'no-resolve' ? t('admin:rules.matchers.no_resolve') : o}</Badge>
        )),
      },
    ]
    if (!readOnly) {
      cols.push({
        id: 'actions', header: '', enableSorting: false, meta: { hideable: false, align: 'right', label: t('admin:rules.table.actions'), className: 'w-20' },
        cell: ({ row }) => (
          <span className="inline-flex items-center justify-end gap-0.5">
            <IconButton label={t('admin:rules.matchers.edit_named', { value: row.original.shown })} size="icon-xs" onClick={() => onEditRow(row.original.id)}><IconEdit /></IconButton>
            <MoreMenu items={rowMenu(row.original)} size="icon-xs" label={t('admin:rules.matchers.row_menu', { value: row.original.shown })} />
          </span>
        ),
      })
    }
    return cols
    // The row handlers are rebuilt with the document.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, readOnly, doc])

  const range = block.kind === 'rules'
    ? (block.count === 1 ? t('admin:rules.blocks.line_single', { line: block.ruleStart + 1 }) : t('admin:rules.blocks.line_range', { from: block.ruleStart + 1, to: block.end }))
    : t('admin:rules.blocks.line_range', { from: block.start + 1, to: block.end })
  const heading = block.kind === 'text' ? t('admin:rules.blocks.text_block', { count: block.count }) : noTarget ? t('admin:rules.blocks.no_target') : block.target
  const retargetCount = block.count
  const wanted = pending !== null && pending !== block.target ? pending : null

  return (
    <section aria-labelledby="rule-block-heading" className="flex min-w-0 flex-col gap-3">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {phone && <IconButton label={t('admin:rules.blocks.back')} onClick={onBack}><IconArrowLeft /></IconButton>}
        <h2 id="rule-block-heading" className="min-w-0 text-base font-semibold break-words">{t('admin:rules.blocks.heading', { index: block.index + 1, target: heading })}</h2>
        {block.fallback && <Badge variant="outline">{t('admin:rules.blocks.fallback')}</Badge>}
        <span className="text-xs text-muted-foreground">{range}</span>
        <span className="text-xs text-muted-foreground tabular-nums">{t('admin:rules.blocks.count', { count: block.count })}</span>
      </header>

      {block.kind === 'text' ? (
        <div className="flex flex-col gap-3">
          <pre className="max-h-[50dvh] overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap">
            {doc.lines.slice(block.start, block.end).map(l => l.raw.replace(/\r$/, '')).join('\n')}
          </pre>
          {!readOnly && <div><Button variant="outline" onClick={() => onEditText(doc.lines.slice(block.start, block.end).map(l => l.id))}>{t('admin:rules.matchers.edit_lines_as_text')}</Button></div>}
        </div>
      ) : (
        <>
          <div className="grid gap-3 lg:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{t('admin:rules.blocks.note')}</span>
              {!readOnly && noteDraft === null && (
                <IconButton label={t('admin:rules.blocks.note_edit')} size="icon-xs" onClick={() => setNoteDraft(note)}><IconEdit /></IconButton>
              )}
            </div>
            {noteDraft === null ? (
              <p className={note ? 'text-sm break-words whitespace-pre-wrap' : 'text-sm text-muted-foreground'}>{note || '—'}</p>
            ) : (
              <div className="flex flex-col gap-2">
                <Textarea value={noteDraft} aria-label={t('admin:rules.blocks.note')} placeholder={t('admin:rules.blocks.note_placeholder')} onChange={e => setNoteDraft(e.target.value)} />
                <div className="flex gap-2">
                  <Button size="sm" onClick={() => { onSetNote(noteDraft); setNoteDraft(null) }}>{t('common:actions.save')}</Button>
                  <Button size="sm" variant="outline" onClick={() => setNoteDraft(null)}>{t('common:actions.cancel')}</Button>
                </div>
              </div>
            )}
          </div>

          {!readOnly && !noTarget && (
            <div className="flex min-w-0 flex-col gap-2">
              <TargetSelect key={block.index + block.target} value={block.target} choices={choices} label={t('admin:rules.blocks.target')} onChange={setPending} />
              {wanted !== null && (
                <Button className="w-fit max-w-full whitespace-normal" onClick={() => { onRetarget(wanted); setPending(null) }}>
                  {t('admin:rules.blocks.change_target', { target: wanted, count: retargetCount })}
                </Button>
              )}
            </div>
          )}
          </div>

          <DataTable<Row>
            aria-label={t('admin:rules.blocks.matchers', { target: heading })}
            data={shown}
            columns={columns}
            getRowId={r => String(r.id)}
            rowLabel={r => r.shown}
            handleRef={table}
            activeRowId={marked}
            pagination={false}
            columnMenu={false}
            maxHeight={TABLE_HEIGHT}
            selectable={!readOnly}
            selection={checked}
            onSelectionChange={setChecked}
            onRowOpen={readOnly ? undefined : r => onEditRow(r.id)}
            toolbar={(
              <FilterBar
                aria-label={t('admin:rules.matchers.filter_label')}
                search={{ value: local, onChange: setLocal, placeholder: t('admin:rules.matchers.search') }}
                facets={typeOptions.length > 1 ? [{ id: 'type', label: t('admin:rules.matchers.filter_type'), options: typeOptions, selected: types, onChange: setTypes }] : []}
                onClearAll={() => { setLocal(''); setTypes([]) }}
              >
                {!readOnly && !noTarget && (
                  <span className="ml-auto flex flex-wrap items-center gap-2">
                    <Button size="sm" onClick={onAddRow}><IconAdd />{t('admin:rules.matchers.add')}</Button>
                    <Button size="sm" variant="outline" onClick={onPaste}><IconFile />{t('admin:rules.matchers.paste')}</Button>
                    <Button size="sm" variant="outline" onClick={onImport}><IconUpload />{t('admin:rules.matchers.import_file')}</Button>
                  </span>
                )}
              </FilterBar>
            )}
            empty={rows.length === 0
              ? <EmptyState compact title={t('admin:rules.matchers.empty')} />
              : <EmptyState compact title={t('admin:rules.matchers.no_match')} action={<Button variant="outline" onClick={() => { setLocal(''); setTypes([]) }}>{t('admin:rules.clear_search')}</Button>} />}
            bulkActions={readOnly ? undefined : (ctx => (
              <>
                {!noTarget && <Button variant="outline" size="sm" onClick={() => { onMoveRows(ctx.ids.map(Number)); setChecked({}) }}>{t('admin:rules.matchers.move_to')}</Button>}
                <Button variant="outline" size="sm" className="text-destructive" onClick={() => { onDeleteRows(ctx.ids.map(Number)); setChecked({}) }}>
                  <IconDelete />{t('admin:rules.matchers.delete_selected', { count: ctx.ids.length })}
                </Button>
              </>
            ))}
            renderCard={(r, ctx) => (
              <div className="flex items-start gap-2 p-3">
                <button type="button" className="min-w-0 flex-1 text-left" disabled={readOnly} onClick={ctx.open}>
                  <span className="block text-xs text-muted-foreground">{typeName(t, r.type, r.known)}</span>
                  <span className="block font-mono text-sm break-all">{r.shown}</span>
                  {r.options.length > 0 && <span className="block text-xs text-muted-foreground">{r.options.map(o => (o.toLowerCase() === 'no-resolve' ? t('admin:rules.matchers.no_resolve') : o)).join(' · ')}</span>}
                </button>
                {!readOnly && <MoreMenu items={rowMenu(r)} size="icon-xs" label={t('admin:rules.matchers.row_menu', { value: r.shown })} />}
              </div>
            )}
          />
          <p className="text-xs text-muted-foreground tabular-nums" role="status">{shown.length === rows.length ? '' : t('admin:rules.matchers.shown_of', { shown: shown.length, total: rows.length })}</p>
        </>
      )}
    </section>
  )
}
