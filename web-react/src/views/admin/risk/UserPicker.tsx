import { useEffect, useMemo, useState } from 'react'
import type { User } from '@/api/types'
import {
  Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList,
} from '@/components/ui/combobox'
import { useQueryScope } from '@/query/useQueryScope'
import { useUserDetail, useUsersList } from '@/query/users'
import { cn } from '@/lib/utils'
import { useRiskT } from './useRiskT'

/** How long the typing must pause before the keyword is searched. */
const DEBOUNCE_MS = 250
/** One page of matches is enough to pick from; narrowing is by typing. */
const PAGE_SIZE = 50

interface Option { id: number; label: string }

function optionOf(u: Pick<User, 'id' | 'upn' | 'display_name'>): Option {
  return { id: u.id, label: u.display_name ? `${u.display_name} (${u.upn})` : u.upn }
}

/**
 * Picks one account by searching the user list on the server: the risk center's own picker, with the
 * behaviour the shared account picker had (a server search because the fleet can be larger than one
 * page; the keyword waits until typing pauses; the picked account keeps its name when the search moves
 * past it, read by id). Built on the new combobox so none of the risk center's files reaches for the old
 * component set; it takes the `id` and aria props a `Field` hands its control.
 */
export function UserPicker({ value, onChange, placeholder, className, id, ...aria }: {
  /** The picked account's id, or null for none. */
  value: number | null
  onChange: (userId: number | null) => void
  placeholder?: string
  className?: string
  id?: string
  'aria-label'?: string
  'aria-describedby'?: string
}) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const [input, setInput] = useState('')
  const [keyword, setKeyword] = useState('')

  useEffect(() => {
    const timer = setTimeout(() => setKeyword(input.trim()), DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [input])

  const { data } = useUsersList(scope, { page: 1, page_size: PAGE_SIZE, keyword })
  const { data: picked } = useUserDetail(scope, value ?? 0)

  const options = useMemo(() => (data?.items ?? []).map(optionOf), [data])
  const selected = useMemo((): Option | null => {
    if (!value) return null
    const listed = options.find(o => o.id === value)
    if (listed) return listed
    return picked && picked.id === value ? optionOf(picked) : { id: value, label: `#${value}` }
  }, [value, options, picked])
  // The picked option is always among the items, or the list cannot show which one is chosen once the
  // search has moved past it.
  const items = selected && !options.some(o => o.id === selected.id) ? [selected, ...options] : options

  return (
    <Combobox
      items={items}
      value={selected}
      // The server already matched the keyword (UPN, display name, email); filtering again by label
      // would drop a match on the email.
      filter={null}
      onValueChange={(v: Option | null) => onChange(v?.id ?? null)}
      onInputValueChange={(text, details) => {
        // Choosing an option writes its label into the box; that is not a new search.
        if (details.reason === 'input-change' || details.reason === 'input-clear') setInput(text)
      }}
      itemToStringLabel={(o: Option) => o.label}
      isItemEqualToValue={(a: Option, b: Option) => a.id === b.id}
    >
      <ComboboxInput
        id={id}
        showClear={selected !== null}
        placeholder={placeholder ?? t('admin:risk_center.pick_user')}
        className={cn('w-full sm:w-64', className)}
        {...aria}
      />
      <ComboboxContent>
        <ComboboxEmpty>{t('admin:risk_center.picker_empty')}</ComboboxEmpty>
        <ComboboxList>{(o: Option) => <ComboboxItem key={o.id} value={o}>{o.label}</ComboboxItem>}</ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
