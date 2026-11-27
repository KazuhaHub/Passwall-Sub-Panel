import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { User } from '@/api/types'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { useQueryScope } from '@/query/useQueryScope'
import { cn } from '@/lib/utils'
import { useUserDetail, useUsersList } from '@/query/users'

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
  /** Fill the parent's width with no ceiling (a phone's single column). */
  fullWidth?: boolean
}

/**
 * Picks one account by searching the user list on the server. The label is the input's accessible
 * name and its placeholder, the same words: callers place it in a toolbar, where a label line above
 * it would change their layout.
 *
 * A server search, not a filter over a preloaded list: the fleet can be
 * larger than one page, and an account past it must still be findable. The
 * keyword is debounced, so typing "alice" asks once, not five times. The
 * picked account keeps its name even when the current search does not
 * return it (a filter read back from the URL): it is read by id
 * (userKeys.detail).
 */
export default function UserAutocomplete({ value, onChange, label, width = 280, fullWidth = false }: UserAutocompleteProps) {
  const { t } = useTranslation(['admin', 'common'])
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

  // The picked option is always among the items, or the list cannot show which one is chosen
  // whenever the search moved past it.
  const all = selected && !options.some(o => o.id === selected.id) ? [selected, ...options] : options
  const name = label ?? t('admin:risk_center.pick_user')

  return (
    // `width` is a ceiling, not a size: a fixed width wider than its container (a grid column that
    // fits the container) spilled over the next field. `max-w-full` alone does not stop that inside
    // a grid item whose track sizes itself to this box.
    <div className={cn('w-full', fullWidth && 'max-w-full')} style={fullWidth ? undefined : { width: '100%', maxWidth: width }}>
      <Combobox
        items={all}
        value={selected}
        // The server already matched the keyword (UPN, display name, email); filtering again here by
        // label would drop a match on the email.
        filter={null}
        onValueChange={(v: Option | null) => onChange(v?.id ?? null)}
        // Only what the person typed (or clearing the box) is a search; the box being refilled with a
        // picked account's name is not.
        onInputValueChange={(v, details) => { if (details.reason === 'input-change' || details.reason === 'input-clear') setInput(v) }}
        isItemEqualToValue={(a: Option, b: Option) => a.id === b.id}
        itemToStringLabel={(o: Option) => o.label}
      >
        <ComboboxInput aria-label={name} placeholder={name} showClear={selected !== null} className="w-full" />
        <ComboboxContent>
          <ComboboxEmpty>{isFetching ? t('common:status.loading') : t('common:empty')}</ComboboxEmpty>
          <ComboboxList>{(o: Option) => <ComboboxItem key={o.id} value={o}><span className="truncate" title={o.label}>{o.label}</span></ComboboxItem>}</ComboboxList>
        </ComboboxContent>
      </Combobox>
    </div>
  )
}
