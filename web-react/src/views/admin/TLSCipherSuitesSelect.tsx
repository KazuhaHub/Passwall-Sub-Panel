import AddIcon from '@mui/icons-material/Add'
import CloseIcon from '@mui/icons-material/Close'
import { Autocomplete, Box, IconButton, TextField, Typography, alpha } from '@mui/material'

// crypto/tls.CipherSuites(), in the same ID-sorted order as Go's official
// source. InsecureCipherSuites() is intentionally excluded: Go documents those
// separately because their primitives or design have known security issues.
export const TLS_CIPHER_SUITES = [
  'TLS_AES_128_GCM_SHA256',
  'TLS_AES_256_GCM_SHA384',
  'TLS_CHACHA20_POLY1305_SHA256',
  'TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA',
  'TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA',
  'TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA',
  'TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA',
  'TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256',
  'TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384',
  'TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256',
  'TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384',
  'TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256',
  'TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256',
] as const

// Keep the form/API representation compatible with Xray (one colon-delimited
// string) while presenting a constrained multi-select to admins. Focusing the
// empty field opens every supported suite. Candidate tags stay visible after
// selection and switch their action from + to ×, so the same row can add or
// remove a suite without closing the menu.
export function TLSCipherSuitesSelect(props: {
  label: string
  helperText: string
  value: string
  onChange: (next: string) => void
}) {
  const selected = props.value
    ? props.value.split(':').map(value => value.trim()).filter(Boolean)
    : []

  return (
    <Autocomplete
      multiple
      openOnFocus
      disableCloseOnSelect
      disableClearable
      size="small"
      options={[...TLS_CIPHER_SUITES]}
      value={selected}
      sx={{
        '& .MuiAutocomplete-inputRoot': {
          position: 'relative', alignItems: 'center', minHeight: 40,
        },
        // The field is a read-only generated preview. Keep the actual input as
        // a transparent focus/click target. With no selection it must remain
        // in normal flow so MUI can measure the field height; once the preview
        // exists it can overlay that content without adding a blank flex row.
        '& .MuiAutocomplete-input': selected.length > 0
          ? {
              position: 'absolute', inset: 0,
              width: '100% !important', height: '100% !important',
              m: '0 !important', p: '0 !important', opacity: 0,
              cursor: 'pointer',
            }
          : {
              width: '100% !important', minWidth: '30px !important',
              opacity: 0, cursor: 'pointer',
            },
        '& .MuiAutocomplete-endAdornment': { zIndex: 1 },
      }}
      slotProps={{
        listbox: {
          sx: {
            display: 'flex', flexWrap: 'wrap', alignItems: 'flex-start', gap: 1, p: 1,
            '& .MuiAutocomplete-option': {
              width: 'auto', minWidth: 0, minHeight: 0, p: 0,
            },
          },
        },
      }}
      onChange={(_, next) => props.onChange(next.join(':'))}
      onKeyDown={(event) => {
        // The selected value is a generated preview, not a text editor. Keep
        // Backspace/Delete from removing values through Autocomplete's default
        // keyboard handling; changes only come from the + / × option buttons.
        if (event.key === 'Backspace' || event.key === 'Delete') {
          event.preventDefault()
          event.stopPropagation()
        }
      }}
      renderValue={(value) => (
        <Typography component="span" title={value.join(':')} sx={{
          width: '100%', minWidth: 0, maxWidth: '100%', py: 0.25,
          whiteSpace: 'normal', overflowWrap: 'anywhere', wordBreak: 'break-word',
          lineHeight: 1.5, fontSize: 13,
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
        }}>
          {value.join(':')}
        </Typography>
      )}
      renderOption={(optionProps, option, state) => (
        <li {...optionProps} key={option}>
          <Box sx={(theme) => ({
            width: 'fit-content', maxWidth: '100%', minWidth: 0,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1,
            px: 1.25, py: 0.25, borderRadius: 4,
            border: `1px solid ${state.selected ? theme.palette.primary.main : theme.palette.divider}`,
            bgcolor: state.selected ? alpha(theme.palette.primary.main, 0.12) : 'transparent',
          })}>
            <Typography component="span" noWrap sx={{
              minWidth: 0, fontSize: 12,
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
            }}>
              {option}
            </Typography>
            <IconButton component="span" size="small" tabIndex={-1}
              aria-label={state.selected ? `Remove ${option}` : `Add ${option}`}
              sx={{ flex: '0 0 auto', p: 0.25 }}>
              {state.selected ? <CloseIcon sx={{ fontSize: 16 }} /> : <AddIcon sx={{ fontSize: 16 }} />}
            </IconButton>
          </Box>
        </li>
      )}
      renderInput={(params) => (
        <TextField {...params} label={props.label} helperText={props.helperText}
          slotProps={{
            ...params.slotProps,
            htmlInput: {
              ...params.slotProps.htmlInput,
              readOnly: true,
              'aria-readonly': true,
            },
          }} />
      )}
    />
  )
}
