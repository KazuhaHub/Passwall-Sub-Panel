import { Autocomplete, Chip, TextField, createFilterOptions } from '@mui/material'

const tagFilter = createFilterOptions<string>()

// TagsAutocomplete is the shared tag input used by the create/edit/import/
// separator dialogs. Wraps a freeSolo multi-select Autocomplete around the
// form's `tags_text` comma-joined string so admins pick from existing tags
// (suggestion dropdown) AND introduce new ones. freeSolo alone only adds on
// Enter (no affordance, dead on touch) — filterOptions surfaces the typed
// value as a clickable "add this" row so a new tag can be SELECTED too, which
// is what the dropdown-only bug was missing. Form state stays a comma-joined
// string for backend compatibility — the split/join happens here.
export default function TagsAutocomplete(props: {
  label: string
  value: string
  options: string[]
  onChange: (next: string) => void
  helperText?: string
  /** Renders 24px chips so a single row keeps a small TextField's 40px height. */
  dense?: boolean
}) {
  const tags = props.value
    ? props.value.split(',').map(s => s.trim()).filter(Boolean)
    : []
  return (
    <Autocomplete
      multiple
      freeSolo
      size="small"
      selectOnFocus
      clearOnBlur
      handleHomeEndKeys
      options={props.options}
      value={tags}
      filterOptions={(options, params) => {
        const filtered = tagFilter(options, params)
        const input = params.inputValue.trim()
        // Offer the typed value as a selectable row when it isn't already a
        // known option (case-insensitive) — so a brand-new tag can be clicked
        // to add, not only committed via Enter.
        if (input && !options.some(o => o.toLowerCase() === input.toLowerCase())) {
          filtered.push(input)
        }
        return filtered
      }}
      onChange={(_, v) => {
        // de-dup + drop empties so two passes through this onChange can't
        // create "Premium,Premium" / trailing commas. Sort is NOT applied
        // so user-controlled ordering survives.
        const seen = new Set<string>()
        const cleaned: string[] = []
        for (const raw of v as string[]) {
          const s = raw.trim()
          if (!s || seen.has(s)) continue
          seen.add(s)
          cleaned.push(s)
        }
        props.onChange(cleaned.join(', '))
      }}
      renderValue={(value, getItemProps) =>
        value.map((option, index) => {
          const tagProps = getItemProps({ index })
          return <Chip {...tagProps} key={option} label={option} size="small" sx={props.dense ? { height: 24 } : undefined} />
        })
      }
      renderInput={(params) => (
        <TextField {...params} label={props.label} helperText={props.helperText} />
      )}
    />
  );
}
