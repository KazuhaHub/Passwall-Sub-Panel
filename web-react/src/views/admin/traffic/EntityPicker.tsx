import { useMemo } from 'react'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'

export interface PickerOption { id: number; label: string }

/**
 * Pick the user or node a trend is drawn for, by typing: either list can be hundreds long, and a native
 * select only jumps by first letter. Option 0 is "everything" and is always first. A chosen id that is
 * not in the loaded list (the list is capped, or still loading) is kept as a labelled stand-in instead
 * of silently reading as "everything", which would show one user's id over everyone's figures.
 */
export function EntityPicker({ allLabel, options, value, onChange, placeholder, emptyLabel, fallbackLabel, id, 'aria-label': ariaLabel }: {
  allLabel: string
  options: readonly PickerOption[]
  value: number
  onChange: (id: number) => void
  placeholder: string
  emptyLabel: string
  fallbackLabel: (id: number) => string
  id?: string
  'aria-label'?: string
}) {
  const items = useMemo<PickerOption[]>(() => {
    const all = { id: 0, label: allLabel }
    const list = [all, ...options]
    return value > 0 && !list.some(o => o.id === value) ? [...list, { id: value, label: fallbackLabel(value) }] : list
  }, [allLabel, options, value, fallbackLabel])
  const selected = items.find(o => o.id === value) ?? items[0]
  return (
    <Combobox
      items={items}
      value={selected}
      onValueChange={(v: PickerOption | null) => { if (v) onChange(v.id) }}
      itemToStringLabel={(o: PickerOption) => o.label}
      isItemEqualToValue={(a: PickerOption, b: PickerOption) => a.id === b.id}
    >
      <ComboboxInput id={id} aria-label={ariaLabel} placeholder={placeholder} />
      <ComboboxContent>
        <ComboboxEmpty>{emptyLabel}</ComboboxEmpty>
        <ComboboxList>{(o: PickerOption) => <ComboboxItem key={o.id} value={o}>{o.label}</ComboboxItem>}</ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
