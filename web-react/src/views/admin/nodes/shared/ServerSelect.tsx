import type { Server } from '@/api/servers'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { useNodesT } from './useNodesT'

const haystack = (s: Server) => [s.name, s.region, ...(s.tags ?? [])].filter(Boolean).join(' ').toLowerCase()

/**
 * Pick one server by typing: a fleet can have dozens, and a native select only jumps by first letter. The search
 * matches the name, the region and the tags. `value` 0 is "none chosen". Takes the id and aria props a `Field`
 * hands its control, so it sits in one like any input.
 */
export function ServerSelect({ servers, value, onChange, onBlur, placeholder, id, disabled, ...aria }: {
  servers: readonly Server[]
  value: number
  onChange: (id: number) => void
  /** The input lost focus: for a form that validates on blur. */
  onBlur?: () => void
  placeholder?: string
  id?: string
  disabled?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
  'aria-required'?: boolean | 'true' | 'false'
}) {
  const t = useNodesT()
  return (
    <Combobox items={servers as Server[]} value={servers.find(s => s.id === value) ?? null} disabled={disabled}
      onValueChange={(v: Server | null) => { if (v) onChange(v.id) }}
      itemToStringLabel={(s: Server) => s.name} isItemEqualToValue={(a: Server, b: Server) => a.id === b.id}
      filter={(s: Server, query: string) => haystack(s).includes(query.trim().toLowerCase())}>
      <ComboboxInput id={id} placeholder={placeholder} disabled={disabled} onBlur={onBlur} {...aria} />
      <ComboboxContent>
        <ComboboxEmpty>{t('nodes.order.move_none')}</ComboboxEmpty>
        <ComboboxList>{(s: Server) => <ComboboxItem key={s.id} value={s}>{s.name}</ComboboxItem>}</ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
