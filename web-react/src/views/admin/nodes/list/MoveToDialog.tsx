import { useMemo, useState } from 'react'
import type { OrderRow } from '@/api/nodes'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/form/Field'
import { useNodesT } from '../shared/useNodesT'
import { otherLinesOf, rowId, type MoveIntent } from './orderMoves'

/** A row as the person reads it in a list of places to put something. */
export function describeRow(row: OrderRow): string {
  return row.kind === 'separator' ? `— ${row.name} —` : row.name
}

/** The row with where it goes, so two rows with the same name can be told apart (design 3.2). */
function optionLabel(row: OrderRow): string {
  if (row.kind === 'separator') return describeRow(row)
  const path = [row.entry?.panel_name, row.landing?.name].filter(Boolean).join(' → ')
  return path && path !== row.name ? `${row.name} · ${path}` : row.name
}

interface Option { value: string; label: string }
const TOP = ''

/**
 * "Move to…" (design 3.2): the non-drag way to move a row. It works under a filter, on a phone and by keyboard,
 * because its target is chosen BY NAME from the complete list, which has none of the ambiguity of a drop between
 * two visible rows. For a direct row it can take the node's other lines along, right behind it.
 */
export function MoveToDialog({ rows, target, onClose, onMove }: {
  rows: readonly OrderRow[]
  target: OrderRow
  onClose: () => void
  onMove: (intent: MoveIntent) => void
}) {
  const t = useNodesT()
  const others = useMemo(() => (target.kind === 'line' && target.line_id === 'direct' ? otherLinesOf(rows, target) : []), [rows, target])
  const [withLines, setWithLines] = useState(false)
  const [after, setAfter] = useState<Option | null>(null)
  const moved = useMemo(() => [rowId(target), ...(withLines ? others : [])], [target, withLines, others])
  const options = useMemo<Option[]>(
    () => [{ value: TOP, label: t('nodes.order.move_top') }, ...rows.filter(r => !moved.includes(rowId(r))).map(r => ({ value: rowId(r), label: optionLabel(r) }))],
    [rows, moved, t],
  )
  // A choice that the "with lines" box has since made impossible (it is now one of the moved rows) is dropped.
  const chosen = after && options.some(o => o.value === after.value) ? after : null

  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('nodes.order.move_title', { name: describeRow(target) })}</DialogTitle>
          <DialogDescription className="sr-only">{t('nodes.order.move_after')}</DialogDescription>
        </DialogHeader>
        <Field label={t('nodes.order.move_after')}>
          {p => (
            <Combobox items={options} value={chosen} onValueChange={v => setAfter((v as Option | null) ?? null)}
              itemToStringLabel={(o: Option) => o.label} isItemEqualToValue={(a: Option, b: Option) => a.value === b.value}>
              <ComboboxInput id={p.id} aria-describedby={p['aria-describedby']} aria-invalid={p['aria-invalid']} placeholder={t('nodes.order.move_pick')} />
              <ComboboxContent>
                <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
                <ComboboxList>{(o: Option) => <ComboboxItem key={o.value} value={o}>{o.label}</ComboboxItem>}</ComboboxList>
              </ComboboxContent>
            </Combobox>
          )}
        </Field>
        {others.length > 0 && (
          <label className="flex items-start gap-2 text-sm">
            <Checkbox checked={withLines} onCheckedChange={v => setWithLines(v === true)} className="mt-0.5" />
            <span>{t('nodes.order.move_with_lines', { count: others.length })}</span>
          </label>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <Button disabled={!chosen} onClick={() => { if (chosen) { onMove({ moved, after: chosen.value === TOP ? null : chosen.value }); onClose() } }}>
            {t('nodes.order.move_confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
