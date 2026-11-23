import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { User } from '@/api/types'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { useQueryScope } from '@/query/useQueryScope'
import { useUserDetail, useUsersList } from '@/query/users'
import { cn } from '@/lib/utils'

/** How long the typing must pause before the keyword is searched. */
const DEBOUNCE_MS = 250
/** One page of matches is enough to pick from; narrowing is by typing. */
const PAGE_SIZE = 50

interface Option { id: number; label: string }

function optionOf(u: Pick<User, 'id' | 'upn' | 'display_name'>): Option {
  return { id: u.id, label: u.display_name ? `${u.display_name} (${u.upn})` : u.upn }
}

export interface UserAutocompleteProps {
  /** The picked account's id, or null for none. */
  value: number | null
  onChange: (userId: number | null) => void
  label?: string
  width?: number
  /** Fill the parent's width instead of a fixed one (a phone's single column). */
  fullWidth?: boolean
}

/**
 * Picks one account by searching the user list on the server.
 *
 * A server search, not a filter over a preloaded list: the fleet can be
 * larger than one page, and an account past it must still be findable. The
 * keyword is debounced, so typing "alice" asks once, not five times. The
 * picked account keeps its name even when the current search does not
 * return it (a filter read back from the URL): it is read by id
 * (userKeys.detail).
 */
export default function UserAutocomplete({ value, onChange, label, width = 280, fullWidth = false }: UserAutocompleteProps) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const [input, setInput] = useState('')
  const [keyword, setKeyword] = useState('')

  useEffect(() => {
    const id = setTimeout(() => setKeyword(input.trim()), DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [input])

  const { data, isFetching } = useUsersList(scope, { page: 1, page_size: PAGE_SIZE, keyword })
  const { data: picked } = useUserDetail(scope, value ?? 0)

  const options = useMemo(() => (data?.items ?? []).map(optionOf), [data])
  const selected = useMemo((): Option | null => {
    if (!value) return null
    const listed = options.find(o => o.id === value)
    if (listed) return listed
    return picked && picked.id === value ? optionOf(picked) : { id: value, label: `#${value}` }
  }, [value, options, picked])

  // The picked option is always among the options, or MUI warns that the
  // value is not one of them whenever the search moved past it.
  const all = selected && !options.some(o => o.id === selected.id) ? [selected, ...options] : options

  return (
    <Combobox
      items={all}
      value={selected}
      // The server already matched the keyword (UPN, display name, email); filtering again here by label
      // would drop a match on the email.
      filter={null}
      onValueChange={(v: Option | null) => onChange(v?.id ?? null)}
      onInputValueChange={(v, details) => { if (details.reason === 'input-change' || details.reason === 'input-clear') setInput(v) }}
      itemToStringLabel={(o: Option) => o.label}
      isItemEqualToValue={(a: Option, b: Option) => a.id === b.id}
    >
      <ComboboxInput
        aria-label={label ?? t('admin:risk_center.pick_user')} placeholder={label ?? t('admin:risk_center.pick_user')}
        showClear={value !== null}
        className={cn('max-w-full', fullWidth && 'w-full')} style={fullWidth ? undefined : { width }} />
      <ComboboxContent>
        <ComboboxEmpty>{isFetching ? '…' : t('admin:access_control.user_picker.no_match')}</ComboboxEmpty>
        <ComboboxList>
          {(o: Option) => <ComboboxItem key={o.id} value={o}>{o.label}</ComboboxItem>}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
