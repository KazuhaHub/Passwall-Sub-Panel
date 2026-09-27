import { useEffect, useMemo, useState } from 'react'
import { Autocomplete, TextField } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { User } from '@/api/types'
import { useQueryScope } from '@/query/useQueryScope'
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
}

/**
 * Picks one account by searching the user list on the server.
 *
 * A server search, not a filter over a preloaded list: the fleet can be
 * larger than one page, and an account past it must still be findable. The
 * keyword is debounced, so typing "alice" asks once, not five times. The
 * picked account keeps its name even when the current search does not
 * return it (a deep link, or a row opened from another tab): it is read by
 * id through the same entry the lookup's own detail reads.
 */
export default function UserAutocomplete({ value, onChange, label, width = 280 }: UserAutocompleteProps) {
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
    <Autocomplete
      size="small"
      options={all}
      value={selected}
      loading={isFetching}
      // The server already matched the keyword (UPN, display name, email);
      // filtering again here by label would drop a match on the email.
      filterOptions={x => x}
      onChange={(_, v) => onChange(v?.id ?? null)}
      onInputChange={(_, v, reason) => { if (reason === 'input' || reason === 'clear') setInput(v) }}
      isOptionEqualToValue={(a, b) => a.id === b.id}
      getOptionLabel={o => o.label}
      sx={{ width, maxWidth: '100%' }}
      renderInput={params => (
        <TextField {...params} label={label ?? t('admin:risk_center.lookup.pick', { defaultValue: '选择用户' })} />
      )}
    />
  )
}
