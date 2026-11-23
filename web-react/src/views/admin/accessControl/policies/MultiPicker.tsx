import type { ReactNode } from 'react'

import {
  Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxItem, ComboboxList, ComboboxValue,
  useComboboxAnchor,
} from '@/components/ui/combobox'

export interface PickOption {
  id: number
  name: string
  /** Replaces the name in the dropdown row (a list's kind and size); the chip always shows the name. */
  row?: ReactNode
  disabled?: boolean
}

/**
 * Pick several of many by typing: the editor's lists, groups and servers. Chips show what is chosen and
 * each has its own remove button. `value` is the chosen ids; ids the options do not contain are not shown,
 * and an edit keeps them: only what the person added or removed changes (the editor prunes deleted groups itself and says so).
 */
export function MultiPicker({ options, value, onChange, emptyText, id, disabled, ...aria }: {
  options: readonly PickOption[]
  value: readonly number[]
  onChange: (ids: number[]) => void
  emptyText: string
  id?: string
  disabled?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}) {
  const anchor = useComboboxAnchor()
  const chosen = options.filter(o => value.includes(o.id))
  return (
    <Combobox multiple items={options as PickOption[]} value={chosen} disabled={disabled}
      onValueChange={v => onChange([...value.filter(id => !options.some(o => o.id === id)), ...(v as PickOption[]).map(o => o.id)])}
      itemToStringLabel={(o: PickOption) => o.name} isItemEqualToValue={(a: PickOption, b: PickOption) => a.id === b.id}>
      <ComboboxChips ref={anchor}>
        <ComboboxValue>
          {(values: PickOption[]) => (
            <>
              {values.map(o => <ComboboxChip key={o.id}>{o.name}</ComboboxChip>)}
              <ComboboxChipsInput id={id} aria-describedby={aria['aria-describedby']} aria-invalid={aria['aria-invalid']} />
            </>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>{emptyText}</ComboboxEmpty>
        <ComboboxList>
          {(o: PickOption) => (
            <ComboboxItem key={o.id} value={o} disabled={o.disabled}>
              <span className="min-w-0 break-words">{o.row ?? o.name}</span>
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
