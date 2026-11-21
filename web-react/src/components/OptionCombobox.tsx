import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'

export interface ComboOption<V extends string | number> {
  value: V
  label: string
  /** A second line under the label, e.g. why the option cannot be used right now. */
  note?: string
}

/**
 * Pick one of many by typing: node and line lists can be long, and a native select only jumps by first letter.
 * `value` null is "nothing chosen"; the clear button puts it back there. It takes the id and aria props a `Field`
 * hands its control, so it sits in one like any input.
 */
export function OptionCombobox<V extends string | number>({ options, value, onChange, placeholder, emptyText, id, disabled, ...aria }: {
  options: readonly ComboOption<V>[]
  value: V | null
  onChange: (next: V | null) => void
  placeholder?: string
  /** Shown when the typed text matches nothing. */
  emptyText: string
  id?: string
  disabled?: boolean
  'aria-label'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}) {
  return (
    <Combobox
      items={options as ComboOption<V>[]}
      value={options.find(o => o.value === value) ?? null}
      disabled={disabled}
      onValueChange={(v: ComboOption<V> | null) => onChange(v ? v.value : null)}
      itemToStringLabel={(o: ComboOption<V>) => o.label}
      isItemEqualToValue={(a: ComboOption<V>, b: ComboOption<V>) => a.value === b.value}
    >
      <ComboboxInput id={id} placeholder={placeholder} disabled={disabled} showClear={value !== null} {...aria} />
      <ComboboxContent>
        <ComboboxEmpty>{emptyText}</ComboboxEmpty>
        <ComboboxList>
          {(o: ComboOption<V>) => (
            <ComboboxItem key={String(o.value)} value={o}>
              <span className="min-w-0 break-words">
                {o.label}
                {o.note && <span className="block text-xs text-muted-foreground">{o.note}</span>}
              </span>
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
